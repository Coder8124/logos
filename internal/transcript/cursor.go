package transcript

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // read-only, against a copy of Cursor's own storage
)

// Cursor keeps its chats in one SQLite file — User/globalStorage/state.vscdb —
// rather than a file per session: a `composerData:<id>` row per conversation
// listing its messages by id, and a `bubbleId:<composerId>:<bubbleId>` row per
// message. Type 1 is the person, type 2 is the model.
//
// This reader exists because the migration ingest most needs to serve, Claude
// Code to Cursor and back, was the one it could not: without it Cursor went
// through interchange.go, which skips with "install txcript" — a third-party
// binary the migrating user does not have and should not have to get.

// LogosCursorStorageEnv overrides the Cursor globalStorage directory, so a test
// points at a fixture instead of the developer's real chat history.
const LogosCursorStorageEnv = "LOGOS_CURSOR_STORAGE"

// cursorStorageFile is the database inside that directory.
const cursorStorageFile = "state.vscdb"

type cursorReader struct{}

func (cursorReader) harness() string { return "cursor" }

func (cursorReader) root() string {
	if v := os.Getenv(LogosCursorStorageEnv); v != "" {
		if fileExists(filepath.Join(v, cursorStorageFile)) {
			return v
		}
		return ""
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	var p string
	switch runtime.GOOS {
	case "darwin":
		p = filepath.Join(h, "Library", "Application Support", "Cursor", "User", "globalStorage")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			p = filepath.Join(appData, "Cursor", "User", "globalStorage")
		}
	default:
		p = filepath.Join(h, ".config", "Cursor", "User", "globalStorage")
	}
	if p != "" && fileExists(filepath.Join(p, cursorStorageFile)) {
		return p
	}
	return ""
}

// cursorPathSep separates the storage file from the chat inside it. The reader
// interface addresses a session by path, and Cursor's sessions are rows, so one
// path names both: everything ingest already does with paths — dedupe, report,
// hand back to `read` — keeps working without a second addressing scheme.
const cursorPathSep = "#"

func (r cursorReader) discover() ([]string, error) {
	root := r.root()
	if root == "" {
		return nil, nil
	}
	db, err := openCursorStorage(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT key, value FROM cursorDiskKV WHERE key LIKE 'composerData:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type chat struct {
		path    string
		created int64
	}
	var chats []chat
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		var c cursorComposer
		if json.Unmarshal(value, &c) != nil {
			continue // a row this version writes differently is not a reason to report none
		}
		// A chat opened and never used carries no messages and is not history.
		if len(c.Headers) == 0 {
			continue
		}
		chats = append(chats, chat{
			path:    filepath.Join(root, cursorStorageFile) + cursorPathSep + strings.TrimPrefix(key, "composerData:"),
			created: c.CreatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].created > chats[j].created })
	out := make([]string, 0, len(chats))
	for _, c := range chats {
		out = append(out, c.path)
	}
	return out, nil
}

// cursorSeconds converts Cursor's createdAt to the seconds every other reader
// produces. Cursor stores milliseconds, and passing them through made a Cursor
// session's clock run a thousand times fast wherever a caller compared it with
// anything else: the guard on whether a transcript ran while this server was
// alive was always true, and a checkpoint promoted from a Cursor chat sorted
// above every real one forever.
//
// Detected by magnitude rather than assumed, so a row an older Cursor wrote in
// seconds is not divided into 1970.
func cursorSeconds(stamp int64) int64 {
	if stamp > cursorNotSeconds {
		return stamp / 1000
	}
	return stamp
}

// cursorNotSeconds is past any Unix second this code will see (year 5138), so a
// stamp above it is milliseconds.
const cursorNotSeconds = 1e11

type cursorComposer struct {
	ComposerID string         `json:"composerId"`
	Name       string         `json:"name"`
	CreatedAt  int64          `json:"createdAt"`
	Headers    []cursorHeader `json:"fullConversationHeadersOnly"`
}

type cursorHeader struct {
	BubbleID string `json:"bubbleId"`
	Type     int    `json:"type"`
}

type cursorBubble struct {
	Type int    `json:"type"`
	Text string `json:"text"`
	// Tool is what Cursor did rather than said. Nearly half the bubbles in a
	// real chat carry one of these and no text at all — they used to be skipped
	// as empty, so every Cursor session harvested as one where nothing ran.
	Tool *cursorTool `json:"toolFormerData"`
}

// cursorTool is one tool call as Cursor stores it. RawArgs and Params hold the
// same call twice, in the model's spelling and the editor's; they disagree
// often enough that both are read — Params is canonical where it exists, and
// RawArgs is the only one that carries a shell command's full text.
type cursorTool struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	RawArgs string `json:"rawArgs"`
	Params  string `json:"params"`
	Error   string `json:"error"`
	// Additional carries the outcome for every call that has no top-level
	// status — a quarter of them. See outcome.
	Additional *cursorToolExtra `json:"additionalData"`
}

type cursorToolExtra struct {
	Status string `json:"status"`
}

// cursorToolArgs is the union of the argument shapes the tools we can attribute
// use. Unknown tools leave every field empty and are recorded as a tool turn
// with no input, which is still evidence that something ran.
type cursorToolArgs struct {
	Command      string `json:"command"`
	TargetFile   string `json:"target_file"`
	FilePath     string `json:"file_path"`
	RelWorkspace string `json:"relativeWorkspacePath"`
	RelPath      string `json:"relative_workspace_path"`
	DirectoryDir string `json:"directoryPath"`
	Query        string `json:"query"`
}

// turn renders one tool call as a Turn. Input is what the harvest reads to list
// commands run and files touched, so it is the command for a shell tool and the
// path for everything else — the same contract the Claude Code and Codex
// readers fill.
func (t *cursorTool) turn() Turn {
	var a, p cursorToolArgs
	// Errors ignored on purpose: a tool whose arguments this version spells
	// differently still happened, and reporting it with an empty Input is more
	// honest than dropping the turn.
	_ = json.Unmarshal([]byte(t.RawArgs), &a)
	_ = json.Unmarshal([]byte(t.Params), &p)

	input := firstNonEmpty(
		p.Command, a.Command,
		p.RelWorkspace, a.TargetFile, a.FilePath, a.RelPath, p.DirectoryDir,
		a.Query,
	)
	return Turn{Role: "tool", Tool: t.Name, Status: t.outcome(), Input: strings.TrimSpace(input)}
}

// outcome maps Cursor's status onto the three values the rest of the pipeline
// reads: "ok", "error", and "" for genuinely unknown — the same three
// codexStatus produces.
//
// Cursor records the outcome in two places and neither is complete. Counted
// over a real chat database: 1585 calls carry no top-level status at all and
// every one of them has additionalData.status "error", while 3189 have a
// top-level "completed" and no additionalData. Reading only the top-level field
// loses 27% of the history to "unknown"; reading only additionalData loses more
// than half.
//
// A failure recorded in either field wins, which is what the disagreements
// need: 92 calls are top-level "completed" with additionalData "error" — the
// call finished, the work in it did not. Treating those as successes would put
// them in front of the next agent as things that worked.
//
// Unknown stays unknown rather than defaulting to error. harvest marks an
// errored command "— failed", and failed is the field the next agent trusts
// most, so guessing there files approaches as ruled out that nothing ever
// observed.
func (t *cursorTool) outcome() string {
	extra := ""
	if t.Additional != nil {
		extra = t.Additional.Status
	}
	if t.Error != "" || isCursorFailure(t.Status) || isCursorFailure(extra) {
		return "error"
	}
	if t.Status == "completed" || extra == "success" {
		return "ok"
	}
	// "loading" and "pending" are calls still in flight when the chat was
	// written, which is not an outcome.
	return ""
}

// isCursorFailure names the statuses that mean the work did not land. Cancelled
// counts: the user stopped it, so it is the "incomplete" codexStatus already
// reads as an error rather than something that ran.
func isCursorFailure(status string) bool {
	return status == "error" || status == "cancelled"
}

func (r cursorReader) read(path string) (*Session, error) {
	file, id, ok := strings.Cut(path, cursorPathSep)
	if !ok {
		return nil, fmt.Errorf("%s does not name a chat inside Cursor's storage (expected <path>%s<chat id>)", path, cursorPathSep)
	}
	db, err := openCursorStorage(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var raw []byte
	if err := db.QueryRow(`SELECT value FROM cursorDiskKV WHERE key = ?`, "composerData:"+id).Scan(&raw); err != nil {
		return nil, fmt.Errorf("no chat %s in %s: %w", id, file, err)
	}
	var c cursorComposer
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("chat %s in %s: %w", id, file, err)
	}

	s := &Session{
		Harness: "cursor",
		ID:      id,
		Path:    path,
		Started: cursorSeconds(c.CreatedAt),
		Ended:   cursorSeconds(c.CreatedAt),
		// Project stays empty on purpose: a composer record carries no working
		// directory, and guessing one from the files a chat happened to mention
		// would attribute someone's history to the wrong repository. Review
		// resolves it, which is what review is for.
	}
	// Hashed over the chat's own messages, not the file: every Cursor chat on a
	// machine lives in this one database, so a file hash would make them all
	// identical and the ingest queue would take the first and skip the rest as
	// already seen.
	content := make([]byte, 0, 1024)
	for _, h := range c.Headers {
		var b []byte
		key := fmt.Sprintf("bubbleId:%s:%s", c.ComposerID, h.BubbleID)
		if err := db.QueryRow(`SELECT value FROM cursorDiskKV WHERE key = ?`, key).Scan(&b); err != nil {
			s.Skipped++ // Cursor prunes message rows while the chat still lists them
			continue
		}
		var bub cursorBubble
		if err := json.Unmarshal(b, &bub); err != nil {
			s.Skipped++
			continue
		}
		text := strings.TrimSpace(bub.Text)
		if text == "" && bub.Tool == nil {
			continue // a bubble that carried only a diff, with nothing to attribute
		}
		content = append(content, key...)
		content = append(content, b...)
		if text != "" {
			role := "assistant"
			if bub.Type == 1 || (bub.Type == 0 && h.Type == 1) {
				role = "user"
			}
			s.Turns = append(s.Turns, Turn{Role: role, Text: text})
		}
		// After the text, not instead of it: a bubble can both say what it is
		// about to do and record the call, and the two are separate turns
		// everywhere else.
		if bub.Tool != nil {
			s.Turns = append(s.Turns, bub.Tool.turn())
		}
	}
	if len(s.Turns) == 0 {
		return nil, fmt.Errorf("chat %s in %s has no readable messages", id, file)
	}
	s.Hash = hashBytes(content)
	return s, nil
}

// openCursorStorage opens the database read-only. Cursor may be running, and a
// reader that takes a write lock on a live editor's state is a bug the user
// experiences as their editor hanging.
func openCursorStorage(dir string) (*sql.DB, error) {
	p := filepath.Join(dir, cursorStorageFile)
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, fmt.Errorf("could not open Cursor's storage at %s: %w", p, err)
	}
	return db, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// SourceFile returns the file on disk that a recorded source path names.
//
// Cursor addresses a session as <storage file>#<chat id>, so a caller asking
// the plain question "is this transcript still there?" cannot just stat the
// string: every Cursor session would read as deleted while its database sat
// untouched. The addressing scheme was invented here, so the answer lives here
// too, rather than in each caller reinventing the split.
func SourceFile(source string) string {
	if file, _, ok := strings.Cut(source, cursorPathSep); ok {
		return file
	}
	return source
}
