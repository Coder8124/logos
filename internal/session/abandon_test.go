package session

import (
	"testing"
	"time"
)

func TestFindAbandonedFlagsSilentOpenSessions(t *testing.T) {
	db := testDB(t)

	old := time.Now().Add(-2 * AbandonAfter).Unix()
	if _, err := AddNoteAt(db, "kestrel-one", "claude", "ruled out the dual-mic drop", old); err != nil {
		t.Fatal(err)
	}

	got, err := FindAbandoned(db, AbandonAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 abandoned session, got %d: %+v", len(got), got)
	}
	if got[0].Project != "kestrel-one" || got[0].Agent != "claude" {
		t.Errorf("unexpected session reported: %+v", got[0])
	}
	if got[0].Notes != 1 {
		t.Errorf("notes = %d, want 1", got[0].Notes)
	}
}

// A session that is actively being worked — recent notes, even if it started
// long ago — must not be flagged. What matters is silence, not age.
func TestFindAbandonedIgnoresRecentActivity(t *testing.T) {
	db := testDB(t)

	longAgo := time.Now().Add(-30 * 24 * time.Hour).Unix()
	if _, err := AddNoteAt(db, "kestrel-one", "claude", "started this a month ago", longAgo); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNote(db, "kestrel-one", "claude", "still working it, just now"); err != nil {
		t.Fatal(err)
	}

	got, err := FindAbandoned(db, AbandonAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a session with recent activity should not be reported abandoned, got %+v", got)
	}
}

// A session with no notes at all — an agent that opened one and never wrote
// anything down — must still be reportable. It is a smaller story than one
// holding findings, but it is still a session that never got handed off.
func TestFindAbandonedIncludesNoteless(t *testing.T) {
	db := testDB(t)

	if _, err := Start(db, "kestrel-one", "claude", "look into the yield drop"); err != nil {
		t.Fatal(err)
	}
	// Backdate the session directly; Start always stamps "now".
	if _, err := db.Exec(`UPDATE sessions SET started = ? WHERE project = 'kestrel-one'`,
		time.Now().Add(-2*AbandonAfter).Unix()); err != nil {
		t.Fatal(err)
	}

	got, err := FindAbandoned(db, AbandonAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Notes != 0 {
		t.Fatalf("want 1 abandoned session with 0 notes, got %+v", got)
	}
}

// A committed session is not abandoned — it was handed off properly, which is
// the entire distinction this feature exists to draw.
func TestFindAbandonedExcludesCommittedSessions(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	old := time.Now().Add(-2 * AbandonAfter).Unix()
	AddNoteAt(db, "kestrel-one", "claude", "yield still low", old)
	if err := Commit(db, dir, &Checkpoint{Project: "kestrel-one", Agent: "claude", Next: "re-run"}); err != nil {
		t.Fatal(err)
	}

	got, err := FindAbandoned(db, AbandonAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a committed session should never be reported abandoned, got %+v", got)
	}
}

func TestFindAbandonedInProjectScopes(t *testing.T) {
	db := testDB(t)
	old := time.Now().Add(-2 * AbandonAfter).Unix()
	AddNoteAt(db, "kestrel-one", "claude", "note on kestrel", old)
	AddNoteAt(db, "orrery", "codex", "note on orrery", old)

	got, err := FindAbandonedInProject(db, "kestrel-one", AbandonAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Project != "kestrel-one" {
		t.Fatalf("scoping to one project leaked another's session: %+v", got)
	}
}
