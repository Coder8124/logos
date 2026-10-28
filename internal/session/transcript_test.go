package session

import (
	"os"
	"path/filepath"
	"testing"
)

// A conversation folder that holds only an archived transcript is not a
// history: it is not a scope to resume, and its transcript is not a checkpoint
// any listing should count.
func TestAnArchivedTranscriptIsNeitherACheckpointNorAScope(t *testing.T) {
	v := t.TempDir()
	only := filepath.Join(v, CheckpointDir, "kestrel", ConvosDir, "4b1e9c2a")
	if err := os.MkdirAll(only, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(only, "transcript.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scopes, err := Scopes(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Errorf("a transcript-only folder made scopes %v", scopes)
	}
	entries, err := CheckpointEntries(filepath.Join(v, CheckpointDir, "kestrel"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the archived transcript was listed as checkpoint(s) %v", entries)
	}
	if got := FindTranscript(v, "4B1E9C2A"); got != filepath.Join(only, "transcript.jsonl") {
		t.Errorf("FindTranscript = %q", got)
	}
	if got := FindTranscript(v, "4b1e9c2"); got != "" {
		t.Errorf("a prefix of the id found %q; the lookup is exact", got)
	}
}
