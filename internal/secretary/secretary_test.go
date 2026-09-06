package secretary

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCommitmentAddIsIdempotent(t *testing.T) {
	db := testDB(t)

	// The same loop extracted twice — from Tuesday's note and Wednesday's —
	// must be one commitment, not two.
	added, err := Add(db, &Commitment{Text: "email Sarah the deck", Who: "Sarah"})
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	added, _ = Add(db, &Commitment{Text: "Email  Sarah   the deck", Who: "sarah"})
	if added {
		t.Error("a fingerprint-equal commitment must not be added twice")
	}

	if n, _ := OpenCount(db); n != 1 {
		t.Errorf("open count = %d, want 1", n)
	}
}

func TestStatusTransitions(t *testing.T) {
	db := testDB(t)
	c := &Commitment{Text: "fix the auth bug"}
	Add(db, c)

	if err := SetStatus(db, c.ID, Done); err != nil {
		t.Fatal(err)
	}
	if n, _ := OpenCount(db); n != 0 {
		t.Errorf("done commitment should not count as open, got %d", n)
	}

	// A dropped loop stays recorded so it is not re-extracted and re-surfaced.
	Add(db, &Commitment{Text: "reply to the vendor"})
	open, _ := Open_(db)
	SetStatus(db, open[0].ID, Dropped)
	if n, _ := OpenCount(db); n != 0 {
		t.Errorf("dropped commitment should not be open, got %d", n)
	}
}

func TestResolvedSinceReturnsOnlyDoneWithinWindow(t *testing.T) {
	db := testDB(t)

	c := &Commitment{Text: "renew the domain"}
	Add(db, c)
	if err := SetStatus(db, c.ID, Done); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolvedSince(db, 0, 1<<62)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Text != "renew the domain" {
		t.Errorf("ResolvedSince = %+v, want the one done commitment", resolved)
	}
}
