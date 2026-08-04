package memory

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func diffDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// log writes a memory_log line directly at a chosen time, so a diff can be
// exercised without a clock.
func logAt(db *sql.DB, ts, memID int64, event, detail string) {
	db.Exec(`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id) VALUES (?,?,?,?,0)`,
		ts, memID, event, detail)
}

func TestDiffBucketsByEvent(t *testing.T) {
	db := diffDB(t)
	logAt(db, 100, 1, EvCreated, "prefers Go")
	logAt(db, 110, 2, EvCreated, "lives in NYC")
	logAt(db, 120, 2, EvSuperseded, "lives in NYC")     // moved
	logAt(db, 121, 3, EvCreated, "lives in Boston")     // the replacement
	logAt(db, 130, 1, EvReinforced, "prefers Go")       // corroborated
	logAt(db, 140, 4, EvForgotten, "temporary wifi pw") // dropped

	res, err := Diff(db, "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 3 {
		t.Errorf("Added = %d, want 3 (two prefs/cities + Boston)", len(res.Added))
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed = %d, want 2 (superseded NYC + forgotten wifi)", len(res.Removed))
	}
	if len(res.Corroborated) != 1 {
		t.Errorf("Corroborated = %d, want 1 (reinforced Go)", len(res.Corroborated))
	}
}

func TestDiffSubjectFilter(t *testing.T) {
	db := diffDB(t)
	logAt(db, 100, 1, EvCreated, "Sarah is the CFO")
	logAt(db, 110, 1, EvSuperseded, "Sarah is the CFO")
	logAt(db, 111, 2, EvCreated, "Sarah is now a recruiter")
	logAt(db, 120, 3, EvCreated, "Alex is sensitive about deadlines")

	res, err := Diff(db, "sarah", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	// Both Sarah "created" lines match, case-insensitively; Alex is excluded.
	if len(res.Added) != 2 || len(res.Removed) != 1 {
		t.Fatalf("subject filter: added=%d removed=%d, want 2/1", len(res.Added), len(res.Removed))
	}
	for _, e := range res.Added {
		if !strings.Contains(strings.ToLower(e.Text), "sarah") {
			t.Errorf("subject filter leaked a non-Sarah entry: %q", e.Text)
		}
	}
	if res.Removed[0].Text != "Sarah is the CFO" {
		t.Errorf("removed the wrong entry: %q", res.Removed[0].Text)
	}
}

func TestDiffWindowExcludesOutside(t *testing.T) {
	db := diffDB(t)
	logAt(db, 50, 1, EvCreated, "old fact before window")
	logAt(db, 150, 2, EvCreated, "in window")
	logAt(db, 250, 3, EvCreated, "after window")

	res, err := Diff(db, "", 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0].Text != "in window" {
		t.Fatalf("window filter failed: %+v", res.Added)
	}
	if res.Empty() {
		t.Fatal("result should not be empty")
	}
}
