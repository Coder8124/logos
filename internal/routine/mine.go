// Package routine finds recurring structure in the episodic tier.
//
// This is deliberately not an LLM problem. "Slack → IDE at 09:40 ±12min on
// weekdays, 78% of the time" is a frequency count; prompting a model to notice
// it is slower, costs more, and is less reliable than the arithmetic. The model
// is used only to *name* a pattern that mining already found.
package routine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/event"
)

// Support thresholds. A pattern must clear all of these before it is ever shown.
//
// Two coincidences are not a routine. A system that claims otherwise burns the
// user's trust faster than it earns it, and trust here is the whole product.
const (
	MinOccurrences = 5
	MinWeeks       = 3
	// MinConsistency is the fraction of eligible days the pattern must appear
	// on. Below this it is a habit you have, not a routine you keep.
	MinConsistency = 0.4
)

// Periodic is a recurring time-of-day pattern for one app.
type Periodic struct {
	App string
	// Weekday true means the pattern holds Mon–Fri; false means weekends.
	Weekday bool
	// MedianStart is seconds past local midnight.
	MedianStart int64
	// SpreadS is the median absolute deviation — how tight the cluster is.
	SpreadS     int64
	Occurrences int
	Weeks       int
	// Consistency is occurrences / eligible days, 0..1.
	Consistency float64
	EventIDs    []int64
}

func (p Periodic) Window() string {
	lo := p.MedianStart - p.SpreadS
	hi := p.MedianStart + p.SpreadS
	return fmt.Sprintf("%s–%s", clock(lo), clock(hi))
}

func (p Periodic) Cadence() string {
	if p.Weekday {
		return "weekdays"
	}
	return "weekends"
}

func clock(secs int64) string {
	secs = ((secs % 86400) + 86400) % 86400
	return fmt.Sprintf("%02d:%02d", secs/3600, (secs%3600)/60)
}

// dayKey identifies a local calendar day.
type dayKey struct{ y, m, d int }

func keyOf(t time.Time) dayKey { return dayKey{t.Year(), int(t.Month()), t.Day()} }

// FindPeriodic looks for apps that are reliably opened around the same time.
//
// Only the first substantial session per app per day counts. Someone opens
// Slack forty times a day; the routine is when they *start*, and counting every
// reopen would swamp the signal.
func FindPeriodic(events []event.Event) []Periodic {
	return findPeriodic(events, func(e event.Event) string {
		if e.Kind != event.Focus || e.DurS < 120 {
			return ""
		}
		return e.App
	})
}

// FindPeriodicSites is the same analysis over browser history.
//
// Worth having separately because it works on day one: browser history reaches
// back months, while focus events only exist from the moment the daemon first
// runs. A user should not have to wait three weeks to see the feature do
// anything.
func FindPeriodicSites(events []event.Event) []Periodic {
	return findPeriodic(events, func(e event.Event) string {
		if e.Kind != event.URL {
			return ""
		}
		return host(e.URL)
	})
}

func host(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}

func findPeriodic(events []event.Event, key func(event.Event) string) []Periodic {
	type sample struct {
		day     dayKey
		offset  int64 // seconds past local midnight
		weekday bool
		id      int64
	}
	firstPerDay := map[string]map[dayKey]sample{}
	eligible := map[bool]map[dayKey]bool{true: {}, false: {}}

	for _, e := range events {
		k := key(e)
		if k == "" {
			continue
		}
		t := time.Unix(e.TS, 0).Local()
		day := keyOf(t)
		wd := t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
		eligible[wd][day] = true

		offset := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
		byDay, ok := firstPerDay[k]
		if !ok {
			byDay = map[dayKey]sample{}
			firstPerDay[k] = byDay
		}
		if prev, seen := byDay[day]; !seen || offset < prev.offset {
			byDay[day] = sample{day, offset, wd, e.ID}
		}
	}

	var out []Periodic
	for app, byDay := range firstPerDay {
		for _, weekday := range []bool{true, false} {
			var offsets []int64
			var ids []int64
			weeks := map[int]bool{}

			for day, s := range byDay {
				if s.weekday != weekday {
					continue
				}
				offsets = append(offsets, s.offset)
				ids = append(ids, s.id)
				t := time.Date(day.y, time.Month(day.m), day.d, 0, 0, 0, 0, time.Local)
				y, w := t.ISOWeek()
				weeks[y*100+w] = true
			}

			if len(offsets) < MinOccurrences || len(weeks) < MinWeeks {
				continue
			}

			med := median(offsets)
			spread := medianAbsDev(offsets, med)

			// A cluster looser than ±90 minutes is not a time-of-day routine,
			// it is just "sometimes in the afternoon".
			if spread > 90*60 {
				continue
			}

			consistency := float64(len(offsets)) / math.Max(1, float64(len(eligible[weekday])))
			if consistency < MinConsistency {
				continue
			}

			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			out = append(out, Periodic{
				App: app, Weekday: weekday,
				MedianStart: med, SpreadS: spread,
				Occurrences: len(offsets), Weeks: len(weeks),
				Consistency: consistency, EventIDs: ids,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Consistency != out[j].Consistency {
			return out[i].Consistency > out[j].Consistency
		}
		return out[i].App < out[j].App
	})
	return out
}

// Sequence is an app-to-app transition that recurs.
type Sequence struct {
	From, To string
	Count    int
	// Share is how often From is followed by To rather than anything else.
	Share    float64
	EventIDs []int64
}

func (s Sequence) String() string {
	return fmt.Sprintf("%s → %s", s.From, s.To)
}

// FindSequences counts adjacent app transitions.
//
// Plain bigram counting rather than PrefixSpan or anything fancier: at vault
// scale the data does not justify it, and a transition table is something the
// user can check against their own memory.
func FindSequences(events []event.Event) []Sequence {
	var focus []event.Event
	for _, e := range events {
		if e.Kind == event.Focus && e.App != "" && e.DurS >= event.IncidentalSecs {
			focus = append(focus, e)
		}
	}
	sort.Slice(focus, func(i, j int) bool { return focus[i].TS < focus[j].TS })

	type pair struct{ from, to string }
	counts := map[pair]int{}
	ids := map[pair][]int64{}
	fromTotals := map[string]int{}

	for i := 1; i < len(focus); i++ {
		prev, cur := focus[i-1], focus[i]
		if prev.App == cur.App {
			continue
		}
		// A transition across a long idle gap is not a transition, it is two
		// separate sessions that happen to be adjacent in the table.
		if cur.TS-(prev.TS+prev.DurS) > 15*60 {
			continue
		}
		p := pair{prev.App, cur.App}
		counts[p]++
		fromTotals[prev.App]++
		if len(ids[p]) < 50 {
			ids[p] = append(ids[p], cur.ID)
		}
	}

	var out []Sequence
	for p, n := range counts {
		if n < MinOccurrences {
			continue
		}
		share := float64(n) / float64(fromTotals[p.from])
		// Below a third, the transition is not characteristic — it is just one
		// of many things that follow.
		if share < 0.33 {
			continue
		}
		out = append(out, Sequence{From: p.from, To: p.to, Count: n, Share: share, EventIDs: ids[p]})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].From < out[j].From
	})
	return out
}

// Anomaly is a departure from the established baseline.
type Anomaly struct {
	App         string
	LastSeenTS  int64
	TypicalGapD float64
	ActualGapD  float64
}

func (a Anomaly) String() string {
	if a.LastSeenTS == 0 {
		return fmt.Sprintf("%s: not seen in the window", a.App)
	}
	return fmt.Sprintf("%s: %.0f days since last use, typically every %.1f",
		a.App, a.ActualGapD, a.TypicalGapD)
}

// FindAnomalies reports apps used regularly in the baseline but absent since.
//
// This is what powers "you haven't touched the API repo in nine days" — the
// single most useful proactive signal, and the one most likely to be wrong if
// the thresholds are loose.
func FindAnomalies(events []event.Event, now int64) []Anomaly {
	lastSeen := map[string]int64{}
	days := map[string]map[dayKey]bool{}

	for _, e := range events {
		if e.Kind != event.Focus || e.App == "" || e.DurS < 300 {
			continue
		}
		if e.TS > lastSeen[e.App] {
			lastSeen[e.App] = e.TS
		}
		if days[e.App] == nil {
			days[e.App] = map[dayKey]bool{}
		}
		days[e.App][keyOf(time.Unix(e.TS, 0).Local())] = true
	}

	var out []Anomaly
	for app, last := range lastSeen {
		used := len(days[app])
		if used < MinOccurrences {
			continue
		}

		span := float64(now-oldest(events)) / 86400
		if span < 14 {
			continue // too little history to call anything unusual
		}
		typical := span / float64(used)
		actual := float64(now-last) / 86400

		// Three times the usual gap, and at least three days — enough to be
		// worth mentioning without firing every time a weekend passes.
		if actual > typical*3 && actual >= 3 {
			out = append(out, Anomaly{App: app, LastSeenTS: last, TypicalGapD: typical, ActualGapD: actual})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ActualGapD > out[j].ActualGapD })
	return out
}

func oldest(events []event.Event) int64 {
	var min int64
	for _, e := range events {
		if min == 0 || e.TS < min {
			min = e.TS
		}
	}
	return min
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func medianAbsDev(v []int64, med int64) int64 {
	if len(v) == 0 {
		return 0
	}
	devs := make([]int64, len(v))
	for i, x := range v {
		d := x - med
		if d < 0 {
			d = -d
		}
		devs[i] = d
	}
	return median(devs)
}

// Slug builds the note name for a periodic routine.
func (p Periodic) Slug() string {
	app := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ' || r == '-' || r == '_':
			return '-'
		}
		return -1
	}, p.App)
	return fmt.Sprintf("routines/%s-%s", app, p.Cadence())
}
