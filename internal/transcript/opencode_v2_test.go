package transcript_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
)

// opencodeV2Tables adds the 2.x tables, with the columns opencode 2.2.6 creates
// that the reader depends on, to a storage dir. Sessions written by 2.x exist
// only here, so a reader that stops at the old tables sees none of them.
func opencodeV2Tables(t *testing.T, dir string) {
	t.Helper()
	opencodeExec(t, dir, `CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_archived INTEGER)`)
	opencodeExec(t, dir, `CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
}

type v2msg struct {
	typ  string
	data map[string]any
}

func opencodeV2Session(t *testing.T, dir, id, parent string, archived bool, updated int64, msgs ...v2msg) {
	t.Helper()
	var p, a any
	if parent != "" {
		p = parent
	}
	if archived {
		a = updated
	}
	opencodeExec(t, dir, `INSERT INTO session_v2 VALUES (?, ?, '/work/logos', ?, ?, ?)`, id, p, updated-1000, updated, a)
	for i, m := range msgs {
		d, _ := json.Marshal(m.data)
		opencodeExec(t, dir, `INSERT INTO session_message VALUES (?, ?, ?, ?, ?, ?, ?)`, id+"_m"+string(rune('a'+i)), id, m.typ, i+1, updated-1000+int64(i), updated, string(d))
	}
}

func v2tool(name, status string, input map[string]any, exit any) map[string]any {
	state := map[string]any{"status": status, "input": input, "metadata": map[string]any{}}
	if exit != nil {
		state["metadata"] = map[string]any{"exit": exit}
	}
	return map[string]any{"type": "tool", "name": name, "state": state}
}

func TestOpencodeSessionsWrittenByVersionTwoAreRead(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := opencodeStorage(t) // the old tables exist but hold nothing of this session
	opencodeV2Tables(t, dir)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeV2Session(t, dir, "ses_new", "", false, 1_794_770_284_000,
		v2msg{"user", map[string]any{"text": "make c.txt then run a failing ls"}},
		v2msg{"assistant", map[string]any{"content": []any{
			map[string]any{"type": "reasoning", "text": "thinking about it"},
			v2tool("write", "completed", map[string]any{"path": "c.txt", "content": "hi"}, nil),
			v2tool("shell", "completed", map[string]any{"command": "ls /nonexistent"}, 1.0),
		}}},
		v2msg{"synthetic", map[string]any{"text": "Called the Read tool with the following input"}},
		v2msg{"assistant", map[string]any{"content": []any{map[string]any{"type": "text", "text": "The ls failed."}}}},
		v2msg{"idle", map[string]any{"outcome": "succeeded"}},
	)

	paths, err := transcript.Sessions("opencode")
	if err != nil || len(paths) != 1 || !strings.HasSuffix(paths[0], "#ses_new") {
		t.Fatalf("Sessions = %v, %v; a session only in the 2.x tables must be found", paths, err)
	}
	s, err := transcript.ReadFile("opencode", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "logos" || s.Ended != 1_794_770_284 || s.Skipped != 0 {
		t.Errorf("project/ended/skipped = %q/%d/%d", s.Project, s.Ended, s.Skipped)
	}
	// reasoning, the synthetic echo and the idle marker are not turns.
	if len(s.Turns) != 4 {
		t.Fatalf("turns = %+v, want user, write, shell, assistant text", s.Turns)
	}
	if w := s.Turns[1]; w.Tool != "write" || w.Input != "c.txt" || w.Status != "ok" {
		t.Errorf("write turn = %+v, want the path from \"path\" and status ok", w)
	}
	if sh := s.Turns[2]; sh.Tool != "shell" || sh.Input != "ls /nonexistent" || sh.Status != "error" {
		t.Errorf("shell turn = %+v, want a command that exited 1 read as failed", sh)
	}
}

func TestOpencodeCommandThatExitedNonZeroIsReadAsFailed(t *testing.T) {
	dir := opencodeStorage(t)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	opencodeSession(t, dir, "ses_1", "", 1_771_995_096_000,
		map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "completed", "input": map[string]any{"command": "false"}, "metadata": map[string]any{"exit": 1}}},
		map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "completed", "input": map[string]any{"command": "true"}, "metadata": map[string]any{"exit": 0}}},
	)
	paths, _ := transcript.Sessions("opencode")
	s, err := transcript.ReadFile("opencode", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Turns[0].Status != "error" || s.Turns[1].Status != "ok" {
		t.Errorf("statuses = %q, %q; want error for exit 1 and ok for exit 0", s.Turns[0].Status, s.Turns[1].Status)
	}
}

func TestOpencodeNewestSessionIsFromWhicheverTablesHoldsTheLatest(t *testing.T) {
	dir := opencodeStorage(t)
	opencodeV2Tables(t, dir)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	text := map[string]any{"type": "text", "text": "x"}
	opencodeSession(t, dir, "ses_old", "", 1_771_000_000_000, text)
	opencodeV2Session(t, dir, "ses_new", "", false, 1_794_000_000_000,
		v2msg{"user", map[string]any{"text": "x"}})

	paths, err := transcript.Sessions("opencode")
	if err != nil || len(paths) != 2 || !strings.HasSuffix(paths[0], "#ses_new") {
		t.Fatalf("paths = %v, %v; want the 2.x session first, the old one still listed", paths, err)
	}
}

func TestOpencodeSessionCopiedIntoV2AsASubagentIsNotOfferedFromTheOldTables(t *testing.T) {
	dir := opencodeStorage(t)
	opencodeV2Tables(t, dir)
	t.Setenv(transcript.LogosOpencodeDataEnv, dir)
	text := map[string]any{"type": "text", "text": "x"}
	opencodeSession(t, dir, "ses_main", "", 1_771_000_000_000, text)
	opencodeSession(t, dir, "ses_dup", "", 1_771_000_900_000, text) // looks top-level in the old copy
	opencodeV2Session(t, dir, "ses_main", "", false, 1_771_000_000_000, v2msg{"user", map[string]any{"text": "x"}})
	opencodeV2Session(t, dir, "ses_dup", "ses_main", false, 1_771_000_900_000, v2msg{"user", map[string]any{"text": "x"}})

	paths, err := transcript.Sessions("opencode")
	if err != nil || len(paths) != 1 || !strings.HasSuffix(paths[0], "#ses_main") {
		t.Fatalf("paths = %v, %v; v2 says ses_dup is a subagent, so the old copy must not resurrect it", paths, err)
	}
}
