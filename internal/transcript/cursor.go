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
		Started: c.CreatedAt,
		Ended:   c.CreatedAt,
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
		if text == "" {
			continue // a bubble that only carried a diff or a tool card
		}
		role := "assistant"
		if bub.Type == 1 || (bub.Type == 0 && h.Type == 1) {
			role = "user"
		}
		content = append(content, key...)
		content = append(content, b...)
		s.Turns = append(s.Turns, Turn{Role: role, Text: text})
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
