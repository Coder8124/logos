package transcript

import (
	"path/filepath"
	"testing"
)

// A Cursor session is a row, addressed as <storage file>#<chat id>. Asking
// whether the transcript still exists has to stat the file, not the whole
// string, or every Cursor candidate reads as deleted while its database is
// sitting there — which is exactly what `logos ingest status` reported first.
func TestASourceNamingAChatInsideCursorStorageResolvesToTheFile(t *testing.T) {
	db := filepath.Join("Application Support", "Cursor", "state.vscdb")
	if got := SourceFile(db + "#007f8269-d239-4463-9150-aabba2e50832"); got != db {
		t.Errorf("a Cursor source did not resolve to its storage file: %q", got)
	}
}

// Every other harness records a plain path, which is already the file.
func TestAPlainTranscriptPathIsItsOwnFile(t *testing.T) {
	p := filepath.Join("home", "alice", "session.jsonl")
	if got := SourceFile(p); got != p {
		t.Errorf("a plain transcript path was mangled: %q", got)
	}
}
