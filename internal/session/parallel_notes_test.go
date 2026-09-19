package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invariant 1: the vault is truth and .logos/index.db is a cache, so anything
// that exists only in the cache is already lost.
//
// Commit folds the notes of its own session and of any that has gone silent
// past ParallelGrace, and deliberately leaves a still-working agent's notes
// alone — but it then deleted the whole project's uncommitted.md, which is the
// only place those notes live in the vault. The working agent's findings
// survived in session_notes and nowhere else, so the next `rm -rf .logos &&
// logos index` — the operation every document calls lossless — dropped them
// without a word.
func TestCheckpointingLeavesAParallelAgentsNotesInTheVault(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)

	// Agent B is working right now: its note is fresh, so Commit must not fold
	// it, and must not delete it either.
	if _, err := AddNote(db, "kestrel", "cursor", "the regulator browns out at 3.1V, not 2.8"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNote(db, "kestrel", "claude", "checkout retries twice, not three times"); err != nil {
		t.Fatal(err)
	}

	c := &Checkpoint{Project: "kestrel", Agent: "claude", Task: "fix the checkout crash", Next: "ship it"}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	// The committing agent's own note belongs in the checkpoint, not the file.
	if !strings.Contains(c.State, "checkout retries twice") {
		t.Errorf("the committing agent's own note was not folded into the checkpoint:\n%s", c.State)
	}

	notes, err := os.ReadFile(notesPath(dir, "kestrel"))
	if err != nil {
		t.Fatalf("a working agent's notes are gone from the vault: %v", err)
	}
	if !strings.Contains(string(notes), "browns out at 3.1V") {
		t.Errorf("the working agent's note is not in the vault after another agent checkpointed:\n%s", notes)
	}
	// Folded notes are in the checkpoint now; leaving them here too would
	// claim as outstanding work that has already been recorded.
	if strings.Contains(string(notes), "checkout retries twice") {
		t.Errorf("a note folded into the checkpoint is still listed as uncommitted:\n%s", notes)
	}
}

// The ordinary case must not regress: with nobody else working, a checkpoint
// takes every note and leaves no file behind claiming outstanding work.
func TestCheckpointingAloneLeavesNoUncommittedFile(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	SetVault(db, dir)

	if _, err := AddNote(db, "kestrel", "claude", "the retry budget is the bug"); err != nil {
		t.Fatal(err)
	}
	c := &Checkpoint{Project: "kestrel", Agent: "claude", Task: "fix the retries", Next: "ship it"}
	if err := Commit(db, dir, c); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(notesPath(dir, "kestrel")); !os.IsNotExist(err) {
		left, _ := os.ReadFile(filepath.Clean(notesPath(dir, "kestrel")))
		t.Errorf("an uncommitted file was left behind after everything was folded (%v):\n%s", err, left)
	}
}
