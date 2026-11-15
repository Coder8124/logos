package transcript

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // read-only, against opencode's own storage
)

// opencode keeps every session in one SQLite file, opencode.db: a `session` row
// per conversation, a `message` row per turn (role in its JSON) and a `part` row
// per piece of a message — text, reasoning, or a tool call with its input and
// outcome.
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
	rows, err := db.Query(`
		SELECT s.id FROM session s
		WHERE s.parent_id IS NULL AND s.time_archived IS NULL
		  AND EXISTS (SELECT 1 FROM message m WHERE m.session_id = s.id)
		ORDER BY s.time_updated DESC, s.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, filepath.Join(root, opencodeDBFile)+opencodePathSep+id)
	}
	return out, rows.Err()
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
		return Turn{Role: "tool", Tool: p.Tool, Status: opencodeOutcome(p.State.Status), Input: strings.TrimSpace(input)}, true
	}
	return Turn{}, false
}

// opencodeOutcome keeps "unknown" distinct from "error": a call still pending
// or running when the session was written has not failed, and files of ruled-out
// approaches built on guesses cost the next agent more than a gap does.
func opencodeOutcome(status string) string {
	switch status {
	case "completed":
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
