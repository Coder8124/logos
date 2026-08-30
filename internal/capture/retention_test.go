package capture

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Raw events are the one thing in brain that exists nowhere else — they are not
// derived from the vault and cannot be rebuilt. The two-tier design says rollups
// extract what matters and the raw stream is then disposable, which is only true
// if something actually disposes of it.
//
// Nothing did. Prune existed and was reachable only from `brain prune --days N`,
// typed by hand. A daemon sampling every five seconds accumulated events for as
// long as it ran, with no default window and no disclosure of the cost.

func openStore(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := InitStore(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertAt(t *testing.T, db *sql.DB, daysAgo int, app string) {
	t.Helper()
	ts := time.Now().AddDate(0, 0, -daysAgo).Unix()
	if err := Insert(db, Event{TS: ts, Kind: Focus, App: app, Title: "a window title"}); err != nil {
		t.Fatal(err)
	}
}

// The window is enforced from both sides: old events go, recent ones stay. A
// prune that removed everything would pass a "table stopped growing" check while
// destroying today's work.
func TestPruneKeepsTheWindowAndDropsThePast(t *testing.T) {
	db := openStore(t)

	for _, d := range []int{0, 1, 29, 31, 90, 400} {
		insertAt(t, db, d, "Ghostty")
	}

	cutoff := time.Now().AddDate(0, 0, -30).Unix()
	n, err := Prune(db, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("pruned %d events, want 3 (31, 90 and 400 days old)", n)
	}

	left, err := Count(db)
	if err != nil {
		t.Fatal(err)
	}
	if left != 3 {
		t.Errorf("%d events survived, want 3 (0, 1 and 29 days old)", left)
	}
}

// Running the daemon for a long time must not grow the table without bound.
// This is the property the whole change exists for.
func TestRetentionBoundsTheTable(t *testing.T) {
	db := openStore(t)

	const window = 30
	// Two hundred days of events arriving, pruned as the daemon would prune them.
	for day := 200; day >= 0; day-- {
		insertAt(t, db, day, "Ghostty")
		cutoff := time.Now().AddDate(0, 0, -window).Unix()
		if _, err := Prune(db, cutoff); err != nil {
			t.Fatal(err)
		}
	}

	n, err := Count(db)
	if err != nil {
		t.Fatal(err)
	}
	// One event per day inside the window, give or take the boundary day.
	if n > window+2 {
		t.Errorf("table holds %d events after 200 days with a %d-day window; "+
			"retention is not bounding it", n, window)
	}
	if n == 0 {
		t.Error("retention pruned everything, including events inside the window")
	}
}

// Footprint is what the daemon prints before it starts recording, so it has to
// be honest on an empty store rather than dividing by zero or inventing a rate.
func TestFootprintOnAnEmptyStore(t *testing.T) {
	db := openStore(t)

	events, bytes, days, err := Footprint(db)
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 || bytes != 0 || days != 0 {
		t.Errorf("empty store reported %d events, %d bytes, %.2f days", events, bytes, days)
	}
}

// With real data it must report a span, which is what makes a weekly projection
// meaningful rather than an extrapolation from ten minutes.
func TestFootprintMeasuresSpanAndSize(t *testing.T) {
	db := openStore(t)

	for _, d := range []int{0, 7, 14} {
		insertAt(t, db, d, "Ghostty")
	}

	events, bytes, days, err := Footprint(db)
	if err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Errorf("events = %d, want 3", events)
	}
	if bytes <= 0 {
		t.Error("reported zero bytes for three stored events")
	}
	if days < 13 || days > 15 {
		t.Errorf("span = %.2f days, want ~14", days)
	}
}
