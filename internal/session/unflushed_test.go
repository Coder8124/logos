package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A working note whose write to the vault failed was in the same position a
// stranded memory was: the one caller who was told exited, and every reader
// after it saw a note indistinguishable from one safely on disk. Notes are
// never reaped by a reindex, so nothing was destroyed — but `rm -rf .logos` is
// documented as safe, and for this note it was not.
func TestAWorkingNoteTheVaultRefusedIsRecordedAsNotYetDurable(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)

	scopeDir := filepath.Join(v, CheckpointDir, "kestrel")
	if err := os.MkdirAll(scopeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scopeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(scopeDir, 0o700) })

	if _, err := AddNote(db, "kestrel", "claude", "the annual toggle belongs in PricingTable"); err == nil {
		t.Fatal("adding a note to an unwritable vault reported success")
	}

	n, err := Unflushed(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d working notes recorded as not yet in the vault, want 1", n)
	}
}

// And the repair: the next reindex writes it out and says so, rather than
// leaving it in the cache for a wipe to take.
func TestReindexingWritesOutAWorkingNoteTheVaultNeverGot(t *testing.T) {
	v := t.TempDir()
	db := boundDB(t, v)

	scopeDir := filepath.Join(v, CheckpointDir, "kestrel")
	if err := os.MkdirAll(scopeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scopeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	stranded := "vitest needs jsdom for the PricingTable test"
	if _, err := AddNote(db, "kestrel", "claude", stranded); err == nil {
		t.Fatal("adding a note to an unwritable vault reported success")
	}
	if err := os.Chmod(scopeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	_, rescued, err := ImportNotes(db, v)
	if err != nil {
		t.Fatal(err)
	}
	if rescued != 1 {
		t.Errorf("reindex rescued %d working notes, want 1", rescued)
	}

	raw, err := os.ReadFile(notesPath(v, "kestrel"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), stranded) {
		t.Errorf("the note is still only in the cache:\n%s", raw)
	}
	if n, _ := Unflushed(db); n != 0 {
		t.Errorf("%d notes still marked as not durable after being written out", n)
	}
}
