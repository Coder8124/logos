package transcript_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/transcript"
	_ "modernc.org/sqlite"
)

// opencodeStorage writes the slice of opencode.db the reader depends on, with
// the column names opencode's own migrations create. Built here rather than
// committed as a blob so the shape is legible in the test that asserts it.
func opencodeStorage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_archived INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func opencodeExec(t *testing.T, dir, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func opencodeSession(t *testing.T, dir, id, parent string, updated int64, parts ...map[string]any) {
	t.Helper()
	var p any
	if parent != "" {
		p = parent
	}
	opencodeExec(t, dir, `INSERT INTO session VALUES (?, ?, '/work/logos', ?, ?, NULL)`, id, p, updated-1000, updated)
	for i, part := range parts {
		role := "assistant"
		if part["_user"] == true {
			role = "user"
			delete(part, "_user")
		}
		md, _ := json.Marshal(map[string]any{"role": role})
		pd, _ := json.Marshal(part)
		mid := id + "_m" + string(rune('a'+i))
		opencodeExec(t, dir, `INSERT INTO message VALUES (?, ?, ?, ?, ?)`, mid, id, updated-1000+int64(i), updated, string(md))
		opencodeExec(t, dir, `INSERT INTO part VALUES (?, ?, ?, ?, ?, ?)`, mid+"_p", mid, id, updated-1000+int64(i), updated, string(pd))
	}
}

func TestOpencodeSessionReadsWithNoTxcriptInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no txcript anywhere
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeSession(t, dir, "ses_1", "", 1_771_995_096_000,
		map[string]any{"_user": true, "type": "text", "text": "raise the timeout"},
		map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "completed", "input": map[string]any{"command": "go test ./..."}}},
	)

	paths, err := transcript.Sessions("opencode")
	if err != nil || len(paths) != 1 {
		t.Fatalf("Sessions = %v, %v; want one session without txcript", paths, err)
	}
	s, err := transcript.ReadFile("opencode", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "logos" || s.Cwd != "/work/logos" {
		t.Errorf("project/cwd = %q/%q, want logos//work/logos", s.Project, s.Cwd)
	}
	if s.Ended != 1_771_995_096 {
		t.Errorf("Ended = %d, want milliseconds converted to seconds", s.Ended)
	}
	if len(s.Turns) != 2 || s.Turns[0].Role != "user" || s.Turns[1].Input != "go test ./..." || s.Turns[1].Status != "ok" {
		t.Errorf("turns = %+v", s.Turns)
	}
}

func TestOpencodeSubagentSessionIsNotTheNewestSession(t *testing.T) {
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	text := map[string]any{"type": "text", "text": "x"}
	opencodeSession(t, dir, "ses_main", "", 1_771_995_000_000, text)
	opencodeSession(t, dir, "ses_child", "ses_main", 1_771_995_900_000, text) // finished last

	paths, err := transcript.Sessions("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "#ses_main") {
		t.Fatalf("paths = %v; a subagent must not be offered as the session", paths)
	}
}

func TestOpencodeToolStillRunningIsNotReadAsFailed(t *testing.T) {
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeSession(t, dir, "ses_1", "", 1_771_995_096_000,
		map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "running", "input": map[string]any{"command": "sleep 99"}}},
		map[string]any{"type": "tool", "tool": "edit", "state": map[string]any{"status": "error", "input": map[string]any{"filePath": "a.go"}}},
		map[string]any{"type": "tool", "tool": "grep", "state": map[string]any{"status": "completed", "input": map[string]any{"pattern": "activity.Dir"}}},
	)
	paths, _ := transcript.Sessions("opencode")
	s, err := transcript.ReadFile("opencode", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Turns[0].Status; got != "" {
		t.Errorf("running tool status = %q, want unknown", got)
	}
	if got := s.Turns[1].Status; got != "error" {
		t.Errorf("failed tool status = %q, want error", got)
	}
	if got := s.Turns[2].Input; got != "" {
		t.Errorf("grep pattern leaked into Input as %q; it would be listed as a file edited", got)
	}
}

func TestOpencodeSessionsHashDifferWhenTheyShareOneDatabase(t *testing.T) {
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeSession(t, dir, "ses_a", "", 1_771_995_000_000, map[string]any{"type": "text", "text": "one"})
	opencodeSession(t, dir, "ses_b", "", 1_771_995_100_000, map[string]any{"type": "text", "text": "two"})
	paths, _ := transcript.Sessions("opencode")
	a, _ := transcript.ReadFile("opencode", paths[0])
	b, _ := transcript.ReadFile("opencode", paths[1])
	if a.Hash == b.Hash {
		t.Fatal("two sessions in one database hashed the same; ingest would skip the second as already seen")
	}
}

func TestOpencodeReaderNeverWritesToItsDatabase(t *testing.T) {
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeSession(t, dir, "ses_1", "", 1_771_995_096_000, map[string]any{"type": "text", "text": "hi"})
	db := filepath.Join(dir, "opencode.db")
	before := fileMtime(t, db)
	paths, _ := transcript.Sessions("opencode")
	if _, err := transcript.ReadFile("opencode", paths[0]); err != nil {
		t.Fatal(err)
	}
	if after := fileMtime(t, db); !after.Equal(before) {
		t.Fatalf("database mtime changed %v -> %v: the reader wrote to a live agent's storage", before, after)
	}
}

func fileMtime(t *testing.T, p string) time.Time {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime()
}
