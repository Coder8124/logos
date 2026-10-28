package index

import (
	"os"
	"path/filepath"
	"testing"
)

// An archived transcript sits in a conversation folder beside the checkpoints
// the index reads. It is a raw copy of everything said in the session, so the
// walk must never make it a note: a note is searchable, packed into context,
// and handed to the next agent — the one place a raw transcript must not go.
func TestTheIndexWalkIgnoresArchivedTranscripts(t *testing.T) {
	v := t.TempDir()
	convo := filepath.Join(v, "sessions", "kestrel", "_convos", "4b1e9c2a")
	if err := os.MkdirAll(convo, 0o700); err != nil {
		t.Fatal(err)
	}
	ckpt := "---\nproject: kestrel\nagent: claude-code\n---\n\n## Done\n- shipped the cart fix\n"
	if err := os.WriteFile(filepath.Join(convo, "20261020-101010-claude-code.md"), []byte(ckpt), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"transcript.jsonl", "transcript.jsonl.partial", "transcript.md.txt"} {
		if err := os.WriteFile(filepath.Join(convo, name), []byte(`{"secret":"sk-live-rawtranscriptword"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := Open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	rep, err := ix.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 1 {
		t.Errorf("indexed %d notes from a folder holding one checkpoint and an archived transcript, want 1", rep.Added)
	}
	var n int
	if err := ix.DB.QueryRow(`SELECT count(*) FROM notes WHERE path LIKE '%/transcript.%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d archived transcript file(s) became notes", n)
	}
}
