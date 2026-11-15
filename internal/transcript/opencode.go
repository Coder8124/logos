package transcript

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // read-only, against opencode's own storage
)

// opencode keeps every session in one SQLite file, opencode.db, and has stored
// them two ways. Before 2.x: a `session` row per conversation, a `message` row
// per turn and a `part` row per piece of one. From 2.x: a `session_v2` row per
// conversation and a `session_message` row per turn whose JSON carries its text
// and tool calls inline — and new sessions are written ONLY there, while the old
// tables keep whatever was there at the upgrade. A reader of just the old tables
// reads every old session and never the one that just ended, which is the one
// shutdown recording is for. Both are read; a session in both is read from v2.
//
// This reader exists because opencode has no hooks that fire reliably, so the
// only checkpoint it gets is the one the model chooses to write, and a model
// forgets. recordUnsaved can record a session from the host's own transcript
// when stdin closes, but it needs a native reader to do that; opencode had only
// the txcript-backed one, so a stock install recorded nothing at all.

// LogosOpencodeDataEnv overrides opencode's data directory, so a test points at
// a fixture instead of the developer's real session history.
const LogosOpencodeDataEnv = "LOGOS_OPENCODE_DATA"

const opencodeDBFile = "opencode.db"

type opencodeReader struct{}

func (opencodeReader) harness() string { return "opencode" }

func (opencodeReader) root() string {
	if v := os.Getenv(LogosOpencodeDataEnv); v != "" {
		if fileExists(filepath.Join(v, opencodeDBFile)) {
			return v
		}
		return ""
	}
	// opencode resolves its data directory through XDG on every platform,
	// macOS included, so ~/Library is the wrong place to look.
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(h, ".local", "share")
	}
	p := filepath.Join(base, "opencode")
	if fileExists(filepath.Join(p, opencodeDBFile)) {
		return p
	}
	return ""
}

// opencodePathSep separates the database from the session inside it, for the
// same reason Cursor's does: the reader interface addresses a session by path.
const opencodePathSep = "#"

// discover lists top-level sessions, most recently active first. A session with
// a parent is a subagent's: it is part of its parent's work, and offering it as
// "the newest session" would let a subagent finishing last be recorded in place
// of the conversation the user was actually in.
func (r opencodeReader) discover() ([]string, error) {
	root := r.root()
	if root == "" {
		return nil, nil
	}
	db, err := openOpencodeStorage(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	type found struct {
		id      string
		updated int64
	}
	var all []found
	collect := func(q string) error {
		rows, err := db.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f found
			if err := rows.Scan(&f.id, &f.updated); err != nil {
				return err
			}
			all = append(all, f)
		}
		return rows.Err()
	}

	hasV2 := opencodeHasTable(db, "session_v2") && opencodeHasTable(db, "session_message")
	legacyFilter := ""
	if hasV2 {
		if err := collect(`
			SELECT s.id, s.time_updated FROM session_v2 s
			WHERE s.parent_id IS NULL AND s.time_archived IS NULL
			  AND EXISTS (SELECT 1 FROM session_message m
			              WHERE m.session_id = s.id AND m.type IN ('user', 'assistant'))`); err != nil {
			return nil, err
		}
		// A session v2 knows is v2's to judge: it may be a subagent or archived
		// there while still looking top-level in its pre-upgrade copy.
		legacyFilter = ` AND s.id NOT IN (SELECT id FROM session_v2)`
	}
	if opencodeHasTable(db, "session") && opencodeHasTable(db, "message") {
		if err := collect(`
			SELECT s.id, s.time_updated FROM session s
			WHERE s.parent_id IS NULL AND s.time_archived IS NULL
			  AND EXISTS (SELECT 1 FROM message m WHERE m.session_id = s.id)` + legacyFilter); err != nil {
			return nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].updated != all[j].updated {
			return all[i].updated > all[j].updated
		}
		return all[i].id > all[j].id
	})
	out := make([]string, 0, len(all))
	for _, f := range all {
		out = append(out, filepath.Join(root, opencodeDBFile)+opencodePathSep+f.id)
	}
	return out, nil
}

func opencodeHasTable(db *sql.DB, name string) bool {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return err == nil && n > 0
}

type opencodeMessage struct {
	Role string `json:"role"`
}

type opencodePart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Tool      string `json:"tool"`
	State     struct {
		Status string `json:"status"`
		Input  struct {
			Command  string `json:"command"`
			FilePath string `json:"filePath"`
		} `json:"input"`
		Metadata struct {
			Exit *float64 `json:"exit"`
		} `json:"metadata"`
	} `json:"state"`
}

// turn renders one part, ok=false for the parts that are not evidence of what
// happened: reasoning is the model talking to itself, and step markers carry no
// content.
func (p opencodePart) turn(role string) (Turn, bool) {
	switch p.Type {
	case "text":
		text := strings.TrimSpace(p.Text)
		if text == "" {
			return Turn{}, false
		}
		// Text opencode wrote into the user's turn — an expanded @file, an
		// injected reminder — is not something the person said.
		if p.Synthetic {
			role = "host"
		}
		return Turn{Role: role, Text: text}, true
	case "tool":
		// Only a command or a path goes in Input: the harvest reads it as one,
		// so a grep pattern there would be listed as a file the session edited.
		input := firstNonEmpty(p.State.Input.Command, p.State.Input.FilePath)
		return Turn{Role: "tool", Tool: p.Tool, Status: opencodeOutcome(p.State.Status, p.State.Metadata.Exit), Input: strings.TrimSpace(input)}, true
	}
	return Turn{}, false
}

// opencodeOutcome keeps "unknown" distinct from "error": a call still pending
// or running when the session was written has not failed, and files of ruled-out
// approaches built on guesses cost the next agent more than a gap does.
//
// A command that exited non-zero is still status "completed" — the tool ran; it
// is the command that failed — so the exit code decides, or every failed command
// would be recorded as a success and nothing would be listed as ruled out.
func opencodeOutcome(status string, exit *float64) string {
	switch status {
	case "completed":
		if exit != nil && *exit != 0 {
			return "error"
		}
		return "ok"
	case "error":
		return "error"
	}
	return ""
}

func (r opencodeReader) read(path string) (*Session, error) {
	file, id, ok := strings.Cut(path, opencodePathSep)
	if !ok {
		return nil, fmt.Errorf("%s does not name a session inside opencode's storage (expected <path>%s<session id>)", path, opencodePathSep)
	}
	db, err := openOpencodeStorage(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if opencodeHasTable(db, "session_v2") {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM session_v2 WHERE id = ?`, id).Scan(&n); err == nil && n > 0 {
			return readOpencodeV2(db, path, file, id)
		}
	}
	var dir string
	var created, updated int64
	if err := db.QueryRow(`SELECT directory, time_created, time_updated FROM session WHERE id = ?`, id).Scan(&dir, &created, &updated); err != nil {
		return nil, fmt.Errorf("no session %s in %s: %w", id, file, err)
	}
	s := &Session{
		Harness: "opencode",
		ID:      id,
		Path:    path,
		Cwd:     dir,
		Started: cursorSeconds(created),
		Ended:   cursorSeconds(updated),
	}
	if dir != "" {
		s.Project = filepath.Base(dir)
	}

	rows, err := db.Query(`
		SELECT m.data, p.data FROM message m
		JOIN part p ON p.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.time_created, m.id, p.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	content := make([]byte, 0, 1024)
	for rows.Next() {
		var md, pd []byte
		if err := rows.Scan(&md, &pd); err != nil {
			return nil, err
		}
		var m opencodeMessage
		var p opencodePart
		if json.Unmarshal(md, &m) != nil || json.Unmarshal(pd, &p) != nil {
			s.Skipped++ // a row this version writes differently is reported, not fatal
			continue
		}
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		if t, ok := p.turn(role); ok {
			s.Turns = append(s.Turns, t)
			content = append(content, pd...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(s.Turns) == 0 {
		return nil, fmt.Errorf("session %s in %s has no readable messages", id, file)
	}
	// Over this session's own parts, not the file: every session on the machine
	// shares one database, so a file hash would make them all identical and the
	// ingest queue would keep the first and skip the rest as already seen.
	s.Hash = hashBytes(content)
	return s, nil
}

// opencodeV2Content is one element of an assistant message's content array.
// Tool input is kept as a map because its shape is the tool's, and one tool
// writing a number where another writes a string must not make the whole
// message unreadable.
type opencodeV2Content struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	State struct {
		Status   string         `json:"status"`
		Input    map[string]any `json:"input"`
		Metadata struct {
			Exit *float64 `json:"exit"`
		} `json:"metadata"`
	} `json:"state"`
}

func opencodeInputString(in map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// readOpencodeV2 reads a session from the 2.x tables. Rows of a kind this
// reader does not know (system notices, compaction markers, idle markers, and
// the synthetic echoes of tool results) carry no evidence of their own — the
// tool calls they echo are already in the assistant messages — and are not
// counted as unreadable.
func readOpencodeV2(db *sql.DB, path, file, id string) (*Session, error) {
	var dir string
	var created, updated int64
	if err := db.QueryRow(`SELECT directory, time_created, time_updated FROM session_v2 WHERE id = ?`, id).Scan(&dir, &created, &updated); err != nil {
		return nil, fmt.Errorf("no session %s in %s: %w", id, file, err)
	}
	s := &Session{
		Harness: "opencode",
		ID:      id,
		Path:    path,
		Cwd:     dir,
		Started: cursorSeconds(created),
		Ended:   cursorSeconds(updated),
	}
	if dir != "" {
		s.Project = filepath.Base(dir)
	}
	rows, err := db.Query(`SELECT type, data FROM session_message WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	content := make([]byte, 0, 1024)
	for rows.Next() {
		var typ string
		var data []byte
		if err := rows.Scan(&typ, &data); err != nil {
			return nil, err
		}
		switch typ {
		case "user":
			var m struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(data, &m) != nil {
				s.Skipped++
				continue
			}
			if text := strings.TrimSpace(m.Text); text != "" {
				s.Turns = append(s.Turns, Turn{Role: "user", Text: text})
				content = append(content, data...)
			}
		case "assistant":
			var m struct {
				Content []opencodeV2Content `json:"content"`
			}
			if json.Unmarshal(data, &m) != nil {
				s.Skipped++
				continue
			}
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					if text := strings.TrimSpace(c.Text); text != "" {
						s.Turns = append(s.Turns, Turn{Role: "assistant", Text: text})
					}
				case "tool":
					// Only a command or a path goes in Input, for the reason
					// the legacy reader gives; 2.x names the path "path" in
					// some tools and "filePath" in others.
					input := firstNonEmpty(opencodeInputString(c.State.Input, "command"), opencodeInputString(c.State.Input, "filePath", "path"))
					s.Turns = append(s.Turns, Turn{Role: "tool", Tool: c.Name, Status: opencodeOutcome(c.State.Status, c.State.Metadata.Exit), Input: input})
				}
			}
			content = append(content, data...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(s.Turns) == 0 {
		return nil, fmt.Errorf("session %s in %s has no readable messages", id, file)
	}
	s.Hash = hashBytes(content)
	return s, nil
}

// openOpencodeStorage opens the database read-only. opencode is usually still
// running when this is called, and a reader that takes a write lock on a live
// agent's state is a bug the user experiences as their agent hanging.
func openOpencodeStorage(dir string) (*sql.DB, error) {
	p := filepath.Join(dir, opencodeDBFile)
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, fmt.Errorf("could not open opencode's storage at %s: %w", p, err)
	}
	return db, nil
}
