package sources

import (
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/event"
)

func TestUpcomingWithinFiltersAndSortsSoonestFirst(t *testing.T) {
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.Local)
	events := []event.Event{
		{Kind: event.Calendar, Title: "later", TS: now.Add(3 * time.Hour).Unix()},
		{Kind: event.Calendar, Title: "standup", TS: now.Add(20 * time.Minute).Unix()},
		{Kind: event.Calendar, Title: "past", TS: now.Add(-1 * time.Hour).Unix()},
		{Kind: event.Focus, Title: "not a meeting", App: "Slack", TS: now.Add(10 * time.Minute).Unix()},
		{Kind: event.Calendar, Title: "1:1", TS: now.Add(90 * time.Minute).Unix()},
	}

	got := UpcomingWithin(events, now, 2*time.Hour)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (standup + 1:1; later is >2h, past is behind, focus excluded)", len(got))
	}
	if got[0].Title != "standup" || got[1].Title != "1:1" {
		t.Errorf("soonest-first ordering wrong: %s then %s", got[0].Title, got[1].Title)
	}
}

func TestUpcomingWithinExcludesNonCalendar(t *testing.T) {
	now := time.Now()
	events := []event.Event{{Kind: event.Focus, App: "Ghostty", TS: now.Add(5 * time.Minute).Unix()}}
	if got := UpcomingWithin(events, now, time.Hour); len(got) != 0 {
		t.Errorf("focus events must never be treated as meetings, got %+v", got)
	}
}
