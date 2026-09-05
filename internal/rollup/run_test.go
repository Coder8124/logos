package rollup

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/event"
)

// captureDB opens an in-memory store through capture.InitStore rather than
// hand-rolling a schema, so this exercises the same table Day() actually
// reads through capture.Range.
func captureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := capture.InitStore(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Day() must not need a model to tell a caller there was nothing to roll up:
// no runtime, no sessions, so there is nothing for a model to summarise, and
// today's code already gets this right without touching rt at all.
func TestDayWithNoActivityNeedsNoRouterEvenWhenNil(t *testing.T) {
	db := captureDB(t)
	vault := t.TempDir()
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	res, err := Day(db, vault, nil, date, false)
	if err != nil {
		t.Fatalf("a day with no captured activity must not error, got: %v", err)
	}
	if res.Sessions != 0 {
		t.Fatalf("expected 0 sessions for a day with no events, got %d", res.Sessions)
	}
}

// Once there is a real session to classify, a nil router (the caller could
// not find a local model runtime) must produce a precise, named error rather
// than a nil-pointer panic reaching NewExtractor/rt.Local() further down.
func TestDayWithActivityAndNoRouterNamesTheRealProblem(t *testing.T) {
	db := captureDB(t)
	vault := t.TempDir()
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).Unix()

	if err := capture.Insert(db, event.Event{
		TS: start, Kind: event.Focus, App: "editor", DurS: 600,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := Day(db, vault, nil, date, false)
	if err == nil {
		t.Fatal("a day with sessions but no model runtime must return an error, not silently succeed or panic")
	}
	if res.Sessions != 1 {
		t.Fatalf("expected the one real session to still be counted, got %d", res.Sessions)
	}
}
