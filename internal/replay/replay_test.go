package replay

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLastSeenRoundTrip(t *testing.T) {
	db := testDB(t)
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	if _, ok := LastSeen(db); ok {
		t.Fatal("expected no marker before first Mark")
	}
	if err := Mark(db, 12345); err != nil {
		t.Fatal(err)
	}
	ts, ok := LastSeen(db)
	if !ok || ts != 12345 {
		t.Fatalf("LastSeen = %d, %v; want 12345, true", ts, ok)
	}
	// Mark again — it updates in place, never a second row.
	if err := Mark(db, 99999); err != nil {
		t.Fatal(err)
	}
	if ts, _ := LastSeen(db); ts != 99999 {
		t.Fatalf("LastSeen after re-mark = %d, want 99999", ts)
	}
}

// First run has no marker, so the window falls back to the default lookback and
// FirstRun is set. A fact learned inside that window shows up under Learned.
func TestComposeFirstRun(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	// A memory created yesterday (well inside the default lookback).
	logAt(db, now.AddDate(0, 0, -1).Unix(), 1, memory.EvCreated, "the user prefers Go")

	res, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FirstRun {
		t.Error("expected FirstRun on a store with no marker")
	}
	if len(res.Learned) != 1 || res.Learned[0].Text != "the user prefers Go" {
		t.Fatalf("Learned = %+v, want the one fact", res.Learned)
	}
}

// After marking a catch-up point, only changes *after* it are reported.
func TestComposeRespectsMarker(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	mark := now.Add(-2 * time.Hour).Unix()
	Mark(db, mark)

	logAt(db, now.Add(-3*time.Hour).Unix(), 1, memory.EvCreated, "before the marker")
	logAt(db, now.Add(-1*time.Hour).Unix(), 2, memory.EvCreated, "after the marker")

	res, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstRun {
		t.Error("marker exists, should not be FirstRun")
	}
	if len(res.Learned) != 1 || res.Learned[0].Text != "after the marker" {
		t.Fatalf("Learned = %+v, want only the post-marker fact", res.Learned)
	}
}

func logAt(db *sql.DB, ts, memID int64, event, detail string) {
	db.Exec(`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id) VALUES (?,?,?,?,0)`,
		ts, memID, event, detail)
}
