package deadend

import (
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/session"
)

// Claude Code runs edits in parallel, each with the hook in its own process.
// Two hooks refreshing a cold cache both list what it knows — nothing — and
// both go on to file the same new checkpoint. That interleaving, replayed in
// order: the second must replace the first one's rows, not add to them.
// Duplicates are hidden from the line the agent reads, but would sit in the
// table until the checkpoint changed.
func TestTwoHooksRefreshingAtOnceFileARulingOnce(t *testing.T) {
	vault := t.TempDir()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project: "kestrel", Agent: "claude-code", Task: "work", Next: "carry on",
		Failed: []string{"streaming the export — internal/parse/reader.go loads the whole file"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.DB.Exec(atEditSchema); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(vault, session.CheckpointDir, "kestrel")
	onDisk, err := stamps(dir)
	if err != nil {
		t.Fatal(err)
	}
	known, err := knownStamps(ix.DB, "kestrel") // the second hook lists first...
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AtPath(ix.DB, vault, "kestrel", "internal/parse/reader.go"); err != nil {
		t.Fatal(err) // ...the first files the checkpoint...
	}
	if err := apply(ix.DB, dir, "kestrel", onDisk, known); err != nil {
		t.Fatal(err) // ...and the second files it from what it listed.
	}

	var n int
	if err := ix.DB.QueryRow(`SELECT COUNT(*) FROM ruling_files`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows for one checkpoint naming one file — a refresh working from an out-of-date listing added to the rows instead of replacing them", n)
	}
}
