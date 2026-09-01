package sources

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/Coder8124/brain/internal/event"
)

// Calendar is read through EventKit via a JXA (JavaScript for Automation)
// script run under osascript — the same "external process rather than a native
// binding" tradeoff the frontmost source makes. It needs no compilation and no
// cgo, and it degrades to nothing when Calendar access is denied, exactly like
// browser history degrades when a file is locked.
//
// Unlike every other source, calendar reaches into the *future*: a meeting that
// has not happened is exactly what a secretary needs to warn you about. Those
// future events are stored with their real start timestamps, so the brief can
// read them back like any other episodic row.

// jxaScript pumps the run loop until the async permission callback returns, then
// returns upcoming events as a JSON string. DAYS is how many days ahead to look.
//
// The whole thing is an IIFE whose return value becomes osascript's result on
// stdout. This matters: JXA's console.log writes to stderr, which exec.Output
// does not capture, so the JSON must be the script's return value instead.
const jxaScript = `(function() {
  ObjC.import('EventKit');
  ObjC.import('Foundation');
  const store = $.EKEventStore.alloc.init;
  let done = false, ok = false;
  const cb = function(g, e) { ok = g; done = true; };
  if (store.requestFullAccessToEventsCompletion) store.requestFullAccessToEventsCompletion(cb);
  else store.requestAccessToEntityTypeCompletion(0, cb);
  const deadline = $.NSDate.dateWithTimeIntervalSinceNow(8);
  while (!done && $.NSDate.date.compare(deadline) < 0)
    $.NSRunLoop.currentRunLoop.runModeBeforeDate($.NSDefaultRunLoopMode, $.NSDate.dateWithTimeIntervalSinceNow(0.05));
  if (!ok) return '[]';
  const now = $.NSDate.date;
  const pred = store.predicateForEventsWithStartDateEndDateCalendars(
    now.dateByAddingTimeInterval(-12*3600), now.dateByAddingTimeInterval(DAYS*24*3600), $());
  const events = store.eventsMatchingPredicate(pred);
  const fmt = $.NSISO8601DateFormatter.alloc.init;
  const out = [];
  for (let i = 0; i < events.count && i < 100; i++) {
    const e = events.objectAtIndex(i);
    out.push({
      title: ObjC.unwrap(e.title) || '',
      start: Math.floor(ObjC.unwrap(e.startDate.timeIntervalSince1970)),
      cal: ObjC.unwrap(e.calendar.title) || '',
      allday: ObjC.unwrap(e.isAllDay) || false
    });
  }
  return JSON.stringify(out);
})()`

type calEvent struct {
	Title  string `json:"title"`
	Start  int64  `json:"start"`
	Cal    string `json:"cal"`
	AllDay bool   `json:"allday"`
}

// CalendarEvents returns events from 12h ago through `days` ahead. The window
// spans a little into the past so a meeting in progress still shows.
func CalendarEvents(days int) ([]event.Event, error) {
	script := replaceAll(jxaScript, "DAYS", fmt.Sprintf("%d", days))

	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return nil, fmt.Errorf("calendar unavailable: %w", err)
	}

	var raw []calEvent
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("calendar returned unparseable output: %w", err)
	}

	events := make([]event.Event, 0, len(raw))
	for _, c := range raw {
		if c.Title == "" {
			continue
		}
		// All-day events are noise for a "what's coming up" brief — they are
		// not appointments you can be late for.
		if c.AllDay {
			continue
		}
		events = append(events, event.Event{
			TS:    c.Start,
			Kind:  event.Calendar,
			App:   c.Cal, // the calendar name lives in App, e.g. "Work", "Family"
			Title: c.Title,
		})
	}
	return events, nil
}

// UpcomingWithin returns calendar events starting within the next d, soonest
// first — the raw material for "standup in 20 minutes".
func UpcomingWithin(events []event.Event, now time.Time, d time.Duration) []event.Event {
	var out []event.Event
	cutoff := now.Add(d).Unix()
	for _, e := range events {
		if e.Kind == event.Calendar && e.TS >= now.Unix() && e.TS <= cutoff {
			out = append(out, e)
		}
	}
	// soonest first
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].TS < out[j-1].TS; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
