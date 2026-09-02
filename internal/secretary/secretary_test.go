package secretary

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/event"
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
	if err := capture.InitStore(db); err != nil {
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

func TestBriefLeadsWithStaleLoops(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.Local)

	// One fresh, one five days old.
	Add(db, &Commitment{Text: "book the venue", Created: now.Add(-1 * time.Hour).Unix()})
	Add(db, &Commitment{Text: "send Q3 numbers", Who: "Priya", Created: now.AddDate(0, 0, -5).Unix()})

	b, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Loops) != 2 {
		t.Fatalf("expected 2 loops, got %d", len(b.Loops))
	}
	// Stalest first.
	if b.Loops[0].Text != "send Q3 numbers" || !b.Loops[0].Stale {
		t.Errorf("brief should lead with the stale loop, got %+v", b.Loops[0])
	}
	if b.Loops[1].Stale {
		t.Error("the one-hour-old loop should not be marked stale")
	}

	head := b.Headline()
	if head == "" || head == "nothing pressing" {
		t.Errorf("headline should surface the stale loop, got %q", head)
	}
}

func TestBriefIsQuietWhenNothingPressing(t *testing.T) {
	db := testDB(t)
	b, err := Compose(db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !b.IsQuiet() {
		t.Errorf("an empty brief should be quiet, got %+v", b)
	}
	if b.Headline() != "nothing pressing" {
		t.Errorf("headline = %q", b.Headline())
	}
}

func TestBriefAnticipatesUsualRoutine(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 20, 9, 40, 0, 0, time.Local) // a Monday-ish morning

	// Build a weekday 09:40 Slack habit across four weeks so it clears the
	// support threshold, landing right on "now".
	var id int64
	for wk := 0; wk < 4; wk++ {
		for d := 0; d < 5; d++ {
			id++
			day := time.Date(2026, 6, 1+wk*7+d, 9, 38+d, 0, 0, time.Local)
			capture.Insert(db, event.Event{TS: day.Unix(), Kind: event.Focus, App: "Slack", DurS: 900})
		}
	}

	b, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range b.Usual {
		if n.Text != "" && contains(n.Text, "Slack") {
			found = true
		}
	}
	if !found {
		t.Errorf("brief should anticipate the usual 09:40 Slack routine, got %+v", b.Usual)
	}
}

func TestGreetingTracksTimeOfDay(t *testing.T) {
	cases := map[int]string{3: "Still up", 9: "Morning", 14: "Afternoon", 19: "Evening", 23: "Late one"}
	for h, want := range cases {
		got := greeting(time.Date(2026, 7, 20, h, 0, 0, 0, time.Local))
		if got != want {
			t.Errorf("greeting at %02d:00 = %q, want %q", h, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestBriefLeadsHeadlineWithImminentMeeting(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.Local)

	// A stale loop and a meeting 10 minutes out. The meeting must win the
	// headline: it is the only item you cannot recover from missing.
	Add(db, &Commitment{Text: "old thing", Created: now.AddDate(0, 0, -5).Unix()})
	capture.Insert(db, capture.Event{Kind: capture.Calendar, Title: "standup", App: "Work",
		TS: now.Add(10 * time.Minute).Unix()})

	b, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Upcoming) != 1 || b.Upcoming[0].Title != "standup" {
		t.Fatalf("expected the standup in upcoming, got %+v", b.Upcoming)
	}
	if !b.Upcoming[0].Imminent {
		t.Error("a meeting 10 minutes out should be imminent")
	}
	if head := b.Headline(); head != "standup in 10m" {
		t.Errorf("headline = %q, want the imminent meeting to lead", head)
	}
}

func TestDistantMeetingDoesNotStealHeadlineFromStaleLoop(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.Local)

	Add(db, &Commitment{Text: "call the bank", Created: now.AddDate(0, 0, -4).Unix()})
	// Two hours out — not imminent, so the stale loop still leads.
	capture.Insert(db, capture.Event{Kind: capture.Calendar, Title: "review", App: "Work",
		TS: now.Add(2 * time.Hour).Unix()})

	b, _ := Compose(db, now)
	if head := b.Headline(); head == "review in 120m" {
		t.Error("a non-imminent meeting must not steal the headline from a stale loop")
	}
}
