package presence

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/secretary"
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
	for _, init := range []func(*sql.DB) error{memory.Init, capture.InitStore, secretary.Init, Init} {
		if err := init(db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func basePrefs() Prefs {
	return Prefs{Interjections: true, LeadMinutes: 10, MinGapMinutes: 60}
}

// A stale open commitment becomes an agenda interjection.
func TestCheckRaisesStaleLoop(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	secretary.Add(db, &secretary.Commitment{
		Text: "email Sarah the deck", Created: now.AddDate(0, 0, -5).Unix(), Status: secretary.Open,
	})

	items, err := Check(db, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found *Interjection
	for i := range items {
		if items[i].Kind == Agenda {
			found = &items[i]
		}
	}
	if found == nil {
		t.Fatalf("expected an agenda interjection, got %+v", items)
	}
	if found.Critical {
		t.Error("an agenda nudge must not be marked critical — only meetings interrupt focus")
	}
}

// The presence defers to deep focus for non-urgent nudges, then speaks once the
// focus passes.
func TestNextDefersToFocus(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	secretary.Add(db, &secretary.Commitment{
		Text: "review the PR", Created: now.AddDate(0, 0, -5).Unix(), Status: secretary.Open,
	})

	if it, _ := Next(db, now, basePrefs(), true); it != nil {
		t.Fatalf("should stay silent during focus, but said: %q", it.Text)
	}
	it, _ := Next(db, now, basePrefs(), false)
	if it == nil {
		t.Fatal("should speak the stale loop once focus passes")
	}
}

// Non-urgent nudges are spaced by the cooldown; the same one is never repeated.
func TestNextCooldownAndDedup(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	secretary.Add(db, &secretary.Commitment{
		Text: "a", Created: now.AddDate(0, 0, -5).Unix(), Status: secretary.Open,
	})
	secretary.Add(db, &secretary.Commitment{
		Text: "b", Created: now.AddDate(0, 0, -6).Unix(), Status: secretary.Open,
	})

	first, _ := Next(db, now, basePrefs(), false)
	if first == nil {
		t.Fatal("expected a first interjection")
	}
	// Immediately after, the cooldown blocks the second even though one is ready.
	if it, _ := Next(db, now.Add(time.Minute), basePrefs(), false); it != nil {
		t.Fatalf("cooldown should suppress a second nudge, but said: %q", it.Text)
	}
	// Past the gap, it speaks — and it's a different loop, not a repeat.
	second, _ := Next(db, now.Add(61*time.Minute), basePrefs(), false)
	if second == nil {
		t.Fatal("expected a second interjection after the cooldown")
	}
	if second.Key == first.Key {
		t.Errorf("repeated the same interjection %q", first.Key)
	}
}

func TestInterjectionsOffStaysSilent(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	secretary.Add(db, &secretary.Commitment{
		Text: "x", Created: now.AddDate(0, 0, -5).Unix(), Status: secretary.Open,
	})
	p := basePrefs()
	p.Interjections = false
	if it, _ := Next(db, now, p, false); it != nil {
		t.Fatalf("interjections off, but said: %q", it.Text)
	}
}

func TestQuietHoursWrapMidnight(t *testing.T) {
	p := Prefs{QuietStart: "22:00", QuietEnd: "08:00"}
	day := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	if !inQuietHours(day(23, 0), p) {
		t.Error("23:00 should be quiet")
	}
	if !inQuietHours(day(2, 0), p) {
		t.Error("02:00 should be quiet")
	}
	if inQuietHours(day(12, 0), p) {
		t.Error("noon should not be quiet")
	}
}
