package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/ingest"
	"github.com/Coder8124/brain/internal/transcript"
)

// Nothing stopped a transcript ingest from writing a live API key straight
// into memories/*.md in plaintext: Harvest copies tool-call text into
// Candidate.Commands verbatim, and Put writes that straight to disk. This is
// the write-path proof for plans/plan0-5-0.md's A2 secret-detection item —
// it fails on the unfixed code because the key survives the round trip to
// disk untouched.
func TestASecretPastedIntoAToolCallNeverReachesTheVaultFile(t *testing.T) {
	s := &transcript.Session{
		Harness: "codex",
		ID:      "01a05124-f309-7cd3-86e6-d0d303880457",
		Project: "gadgets",
		Turns: []transcript.Turn{
			{Role: "tool", Tool: "exec", Status: "ok",
				Input: "curl -H 'Authorization: Bearer sk-live-abcdefghijklmnopqrstuvwxyzAB' https://api.example.com/deploy"},
		},
	}
	c := ingest.Harvest(s)

	v := t.TempDir()
	res, err := ingest.Put(v, c)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(v, filepath.FromSlash(res.Path)))
	if err != nil {
		t.Fatalf("reading the written candidate: %v", err)
	}
	if strings.Contains(string(raw), "sk-live-abcdefghijklmnopqrstuvwxyzAB") {
		t.Fatalf("the candidate note on disk still contains the raw secret:\n%s", raw)
	}
	if len(res.Redactions) == 0 {
		t.Fatal("Put did not report the redaction — invariant 3 says a caught secret must be announced")
	}
}
