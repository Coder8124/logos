package routine

import (
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/event"
)

// at builds a focus event on a given day at a given local time.
func at(day int, hour, minute int, app string, dur int64, id int64) event.Event {
	t := time.Date(2026, 4, day, hour, minute, 0, 0, time.Local)
	return event.Event{ID: id, TS: t.Unix(), Kind: event.Focus, App: app, DurS: dur}
}

// weekdaysIn returns the first n weekday dates in April 2026.
func weekdaysIn(n int) []int {
	var out []int
	for d := 1; len(out) < n && d <= 30; d++ {
		wd := time.Date(2026, 4, d, 0, 0, 0, 0, time.Local).Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			out = append(out, d)
		}
	}
	return out
}

func TestFindPeriodicDetectsAConsistentMorningRoutine(t *testing.T) {
	var events []event.Event
	var id int64
	// Slack at ~09:40 every weekday for four weeks.
	for i, d := range weekdaysIn(20) {
		id++
		events = append(events, at(d, 9, 35+i%10, "Slack", 900, id))
	}

	got := FindPeriodic(events)
	if len(got) == 0 {
		t.Fatal("a four-week consistent pattern should be detected")
	}

	p := got[0]
	if p.App != "Slack" {
		t.Errorf("app = %q", p.App)
	}
	if !p.Weekday {
		t.Error("should be flagged as a weekday routine")
	}
	if p.MedianStart < 9*3600 || p.MedianStart > 10*3600 {
		t.Errorf("median start %s outside the expected morning window", clock(p.MedianStart))
	}
	if p.Weeks < MinWeeks {
		t.Errorf("weeks = %d, want >= %d", p.Weeks, MinWeeks)
	}
	if len(p.EventIDs) == 0 {
		t.Error("a routine must carry the evidence behind it")
	}
}

func TestTwoCoincidencesAreNotARoutine(t *testing.T) {
	events := []event.Event{
		at(1, 9, 40, "Slack", 900, 1),
		at(2, 9, 41, "Slack", 900, 2),
	}
	if got := FindPeriodic(events); len(got) != 0 {
		t.Errorf("two occurrences must not become a routine, got %+v", got)
	}
}

func TestSpreadOutTimesAreNotATimeOfDayRoutine(t *testing.T) {
	var events []event.Event
	var id int64
	// Same app every weekday, but anywhere from 08:00 to 20:00.
	for i, d := range weekdaysIn(20) {
		id++
		events = append(events, at(d, 8+(i*3)%12, 0, "Chrome", 900, id))
	}
	for _, p := range FindPeriodic(events) {
		if p.App == "Chrome" {
			t.Errorf("a 12-hour spread is not a time-of-day routine: %s ±%ds", p.Window(), p.SpreadS)
		}
	}
}

func TestPeriodicIgnoresBriefOpens(t *testing.T) {
	var events []event.Event
	var id int64
	for _, d := range weekdaysIn(20) {
		id++
		events = append(events, at(d, 9, 40, "Finder", 30, id)) // under 120s
	}
	if got := FindPeriodic(events); len(got) != 0 {
		t.Errorf("brief opens should not form a routine, got %+v", got)
	}
}

func TestFindSequencesDetectsACommonTransition(t *testing.T) {
	var events []event.Event
	var id int64
	for _, d := range weekdaysIn(10) {
		id++
		events = append(events, at(d, 9, 40, "Slack", 600, id))
		id++
		events = append(events, at(d, 9, 52, "GoLand", 3600, id))
	}

	got := FindSequences(events)
	if len(got) == 0 {
		t.Fatal("a repeated Slack → GoLand transition should be found")
	}
	if got[0].From != "Slack" || got[0].To != "GoLand" {
		t.Errorf("got %s, want Slack → GoLand", got[0])
	}
	if got[0].Share < 0.9 {
		t.Errorf("share = %.2f, want near 1.0 when it always follows", got[0].Share)
	}
}

func TestSequencesIgnoreTransitionsAcrossIdleGaps(t *testing.T) {
	var events []event.Event
	var id int64
	for _, d := range weekdaysIn(10) {
		id++
		events = append(events, at(d, 9, 0, "Slack", 60, id))
		id++
		// Four hours later — not a transition, a separate session.
		events = append(events, at(d, 13, 0, "GoLand", 3600, id))
	}
	for _, s := range FindSequences(events) {
		if s.From == "Slack" && s.To == "GoLand" {
			t.Error("a four-hour gap must not count as a transition")
		}
	}
}

func TestFindAnomaliesFlagsAnAbsence(t *testing.T) {
	var events []event.Event
	var id int64
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.Local)

	// Used daily for three weeks, then never again.
	for i := 0; i < 21; i++ {
		id++
		ts := base.AddDate(0, 0, i)
		events = append(events, event.Event{ID: id, TS: ts.Unix(), Kind: event.Focus, App: "GoLand", DurS: 3600})
	}
	// "Now" is two weeks after the last use.
	now := base.AddDate(0, 0, 35).Unix()

	got := FindAnomalies(events, now)
	if len(got) == 0 {
		t.Fatal("an app used daily then dropped for two weeks should be flagged")
	}
	if got[0].App != "GoLand" {
		t.Errorf("app = %q", got[0].App)
	}
}

func TestNoAnomalyWithoutEnoughHistory(t *testing.T) {
	var events []event.Event
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.Local)
	for i := 0; i < 6; i++ {
		events = append(events, event.Event{
			ID: int64(i), TS: base.AddDate(0, 0, i).Unix(),
			Kind: event.Focus, App: "GoLand", DurS: 3600,
		})
	}
	// Only a week of history — not enough to call anything unusual.
	if got := FindAnomalies(events, base.AddDate(0, 0, 8).Unix()); len(got) != 0 {
		t.Errorf("should not flag anomalies on thin history, got %+v", got)
	}
}

func TestWindowAndSlugFormatting(t *testing.T) {
	p := Periodic{App: "Google Chrome", Weekday: true, MedianStart: 9*3600 + 40*60, SpreadS: 12 * 60}
	if got := p.Window(); got != "09:28–09:52" {
		t.Errorf("Window = %q", got)
	}
	if got := p.Slug(); got != "routines/google-chrome-weekdays" {
		t.Errorf("Slug = %q", got)
	}
	if got := p.Cadence(); got != "weekdays" {
		t.Errorf("Cadence = %q", got)
	}
}

func TestEmptyInputIsSafe(t *testing.T) {
	if len(FindPeriodic(nil)) != 0 || len(FindSequences(nil)) != 0 || len(FindAnomalies(nil, 0)) != 0 {
		t.Error("empty input must yield no patterns and must not panic")
	}
}
