package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestACodexRolloutIsIdentifiedByTheUuidInItsNameAndLocatedByItsSessionMeta(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-2026-01-05T14-30-00-01a05124-f309-7cd3-86e6-d0d303880456.jsonl")
	body := `{"type":"session_meta","payload":{"id":"01a05124-f309-7cd3-86e6-d0d303880456","cwd":"/Users/bob/src/gadgets"}}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	id, cwd := Locate(p)
	if id != "01a05124-f309-7cd3-86e6-d0d303880456" || cwd != "/Users/bob/src/gadgets" {
		t.Errorf("Locate = %q, %q", id, cwd)
	}
}

// Cursor keeps every chat in one database: the chat is the part after '#', and
// the database says nothing about a folder.
func TestACursorSourceIsIdentifiedByItsComposerIdAndHasNoFolder(t *testing.T) {
	id, cwd := Locate("/x/state.vscdb#composer-123")
	if id != "composer-123" || cwd != "" {
		t.Errorf("Locate = %q, %q", id, cwd)
	}
}

func TestAClaudeCodeTranscriptIsIdentifiedByItsFileStem(t *testing.T) {
	p := filepath.Join(t.TempDir(), "abc-123.jsonl")
	if err := os.WriteFile(p, []byte(`{"cwd":"/w/p"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if id, cwd := Locate(p); id != "abc-123" || cwd != "/w/p" {
		t.Errorf("Locate = %q, %q", id, cwd)
	}
}
