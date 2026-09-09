package session

import (
	"strings"
	"testing"
	"time"
)

// The real failure mode this exists to guard against: closing a session that
// is merely slow, not dead. Refuse anything not already past AbandonAfter,
// even if the caller believes otherwise.
func TestCloseAbandonedRefusesASessionStillWithinTheWindow(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	n, err := AddNote(db, "kestrel-one", "claude", "still working this")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := CloseAbandoned(db, dir, n.Session); err == nil {
		t.Fatal("expected an error closing a session that has not gone silent")
	}
}

func TestCloseAbandonedRefusesAnUnknownID(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	if _, err := CloseAbandoned(db, dir, "20260101-000000-nobody"); err == nil {
		t.Fatal("expected an error closing a session id that does not exist")
	}
}

func TestCloseAbandonedRefusesAnAlreadyClosedSession(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	old := time.Now().Add(-2 * AbandonAfter).Unix()
	n, err := AddNoteAt(db, "kestrel-one", "claude", "ruled out the dual-mic drop", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CloseAbandoned(db, dir, n.Session); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if _, err := CloseAbandoned(db, dir, n.Session); err == nil {
		t.Fatal("expected an error closing an already-closed session")
	}
}

// The core path: a genuinely abandoned session gets a checkpoint, explicitly
// marked as machine-written, carrying its own notes — and the session table
// says it is no longer open.
func TestCloseAbandonedWritesAnAutoClosedCheckpointCarryingItsNotes(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	old := time.Now().Add(-2 * AbandonAfter).Unix()
	n, err := AddNoteAt(db, "kestrel-one", "claude", "ruled out the dual-mic drop", old)
	if err != nil {
		t.Fatal(err)
	}

	c, err := CloseAbandoned(db, dir, n.Session)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AutoClosed {
		t.Error("checkpoint should be marked AutoClosed")
	}
	if !strings.Contains(c.State, "dual-mic drop") {
		t.Errorf("checkpoint state should carry the session's own note, got: %q", c.State)
	}
	if c.Slug == "" {
		t.Error("checkpoint should have a vault slug")
	}

	s, ok, err := Get(db, n.Session)
	if err != nil || !ok {
		t.Fatalf("Get after close: %v, ok=%v", err, ok)
	}
	if s.Ended == 0 {
		t.Error("session should be marked ended after CloseAbandoned")
	}
	if s.Slug != c.Slug {
		t.Errorf("session.Slug = %q, want the checkpoint's slug %q", s.Slug, c.Slug)
	}

	// Invariant 1: the vault, not the index, is what makes this durable — the
	// checkpoint must be sitting on disk as an ordinary markdown note.
	hist, err := History(dir, "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || !hist[0].AutoClosed {
		t.Fatalf("checkpoint did not round-trip through the vault as AutoClosed: %+v", hist)
	}
}

// The scoping guarantee that makes this safe to point at a shared project: a
// different agent's own still-open session, and its uncommitted notes, must
// survive untouched.
func TestCloseAbandonedLeavesASiblingSessionsNotesAlone(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	old := time.Now().Add(-2 * AbandonAfter).Unix()
	dead, err := AddNoteAt(db, "kestrel-one", "claude", "died mid-task", old)
	if err != nil {
		t.Fatal(err)
	}
	live, err := AddNote(db, "kestrel-one", "codex", "still actively working this")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := CloseAbandoned(db, dir, dead.Session); err != nil {
		t.Fatal(err)
	}

	// The live session must still be open and its note still reported as
	// uncommitted — CloseAbandoned closed one session, not the whole project.
	s, ok, err := Get(db, live.Session)
	if err != nil || !ok {
		t.Fatalf("Get for the live session: %v, ok=%v", err, ok)
	}
	if s.Ended != 0 {
		t.Error("a different agent's own open session must not be closed as a side effect")
	}
	uncommitted, err := Uncommitted(db, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(uncommitted) != 1 || uncommitted[0].Text != "still actively working this" {
		t.Errorf("the live session's note should survive, got: %+v", uncommitted)
	}
}
