package secretary

import (
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/event"
)

func TestWeeklyReviewAggregatesTheWeek(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC) // a Sunday
	twoDaysAgo := now.AddDate(0, 0, -2).Unix()

	// Focus time this week: 2h in Code, 1h in Chrome.
	capture.InsertMany(db, []event.Event{
		{TS: twoDaysAgo, Kind: event.Focus, App: "Code", Title: "main.go", DurS: 7200},
		{TS: twoDaysAgo, Kind: event.Focus, App: "Chrome", Title: "docs", DurS: 3600},
		{TS: twoDaysAgo, Kind: event.URL, App: "Chrome", URL: "https://github.com/x/y", DurS: 1800},
		{TS: twoDaysAgo, Kind: event.Commit, App: "git", Title: "fix the parser\n\nbody", Path: "/src/brainrepo"},
		{TS: twoDaysAgo, Kind: event.Calendar, Title: "1:1 with Dana", DurS: 1800},
	})

	// A people note so meeting titles can be attributed to a real person.
	db.Exec(`CREATE TABLE notes (slug TEXT PRIMARY KEY, path TEXT, title TEXT, kind TEXT, body TEXT, hash TEXT, first_seen INTEGER DEFAULT 0)`)
	db.Exec(`CREATE TABLE aliases (slug TEXT, alias TEXT)`)
	db.Exec(`INSERT INTO notes (slug,title,kind,first_seen) VALUES ('people/dana','Dana','person',0)`)

	// A loop closed this week → accomplished; an open one → unfinished + deadline.
	done := &Commitment{Text: "send the deck", Who: "Dana"}
	Add(db, done)
	SetStatus(db, done.ID, Done)
	// SetStatus stamps wall-clock time; pin it inside the review window so the
	// test is independent of when it runs.
	db.Exec("UPDATE commitments SET resolved_at = ? WHERE id = ?", twoDaysAgo, done.ID)
	open := &Commitment{Text: "review the budget", Who: "Sam", DueHint: "friday", Created: twoDaysAgo}
	Add(db, open)

	// A calendar event in the coming week → a dated deadline.
	capture.InsertMany(db, []event.Event{
		{TS: now.AddDate(0, 0, 2).Unix(), Kind: event.Calendar, Title: "Board meeting"},
	})

	r, err := Review(db, now)
	if err != nil {
		t.Fatal(err)
	}

	if r.Stats.Commits != 1 {
		t.Errorf("commits = %d, want 1", r.Stats.Commits)
	}
	if r.Stats.LoopsClosed != 1 || r.Stats.LoopsOpen != 1 {
		t.Errorf("loops closed/open = %d/%d, want 1/1", r.Stats.LoopsClosed, r.Stats.LoopsOpen)
	}
	if !hasItem(r.Accomplished, "send the deck") || !hasItem(r.Accomplished, "fix the parser") {
		t.Errorf("accomplished should include the closed loop and the commit: %+v", r.Accomplished)
	}
	if !hasItem(r.Unfinished, "review the budget") {
		t.Errorf("unfinished should include the open loop: %+v", r.Unfinished)
	}
	// Active hours ≈ 3 (2h + 1h focus).
	if r.Stats.ActiveHours < 2.9 || r.Stats.ActiveHours > 3.1 {
		t.Errorf("active hours = %.2f, want ~3", r.Stats.ActiveHours)
	}
	// Top topic is Code (most focus time).
	if len(r.Topics) == 0 || r.Topics[0].Label != "Code" {
		t.Errorf("top topic should be Code, got %+v", r.Topics)
	}
	// The dated Board meeting deadline should sort ahead of the fuzzy "friday".
	if len(r.Deadlines) == 0 || r.Deadlines[0].Text != "Board meeting" {
		t.Errorf("dated deadline should lead, got %+v", r.Deadlines)
	}
	// People include Dana (loop + meeting) and Sam (loop).
	if !hasPersonStat(r.People, "Dana") || !hasPersonStat(r.People, "Sam") {
		t.Errorf("people should include Dana and Sam: %+v", r.People)
	}
	// Dana appears via a loop and a meeting, so should outrank Sam.
	if r.People[0].Name != "Dana" {
		t.Errorf("most-involved should be Dana, got %+v", r.People)
	}
}

func TestReviewEmptyWeekRecommendsNothingShipped(t *testing.T) {
	db := testDB(t)
	r, err := Review(db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range r.Recommendations {
		if len(rec) > 0 && (containsSub(rec, "Quiet week")) {
			found = true
		}
	}
	if !found {
		t.Errorf("an empty week should note nothing shipped, got %+v", r.Recommendations)
	}
}

func hasItem(items []ReviewItem, text string) bool {
	for _, it := range items {
		if it.Text == text {
			return true
		}
	}
	return false
}
func hasPersonStat(ps []PersonStat, name string) bool {
	for _, p := range ps {
		if p.Name == name {
			return true
		}
	}
	return false
}
func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
