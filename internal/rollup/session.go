// Package rollup turns episodic events into proposed vault notes.
//
// The contract of the two-tier design lives here: raw events go in, notes the
// user has approved come out, and nothing is written to markdown that cannot be
// traced back to specific observed rows.
package rollup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/event"
)

// IdleGapS is how long a silence has to run before the next activity counts as
// a new session rather than a continuation. Fifteen minutes is long enough to
// survive a coffee break and short enough to separate genuinely different
// stretches of work.
const IdleGapS int64 = 15 * 60

// Session is a contiguous stretch of activity. This is the unit the model
// reasons about — never individual events, which carry no context, and never a
// whole day, which exceeds what a small local model handles well.
type Session struct {
	Start, End int64
	Events     []event.Event
	// EventIDs is the evidence set. Every proposal derived from this session
	// cites these.
	EventIDs []int64
}

func (s Session) DurS() int64 { return s.End - s.Start }

// Apps returns time spent per app, descending.
func (s Session) Apps() []string {
	totals := map[string]int64{}
	for _, e := range s.Events {
		if e.Kind == event.Focus && e.App != "" {
			totals[e.App] += e.DurS
		}
	}
	apps := make([]string, 0, len(totals))
	for a := range totals {
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool {
		if totals[apps[i]] != totals[apps[j]] {
			return totals[apps[i]] > totals[apps[j]]
		}
		return apps[i] < apps[j]
	})
	return apps
}

// Sessionise groups events into sessions separated by idle gaps.
//
// Incidental focus flickers are dropped here rather than downstream: they add
// noise to the model's context without adding information, and a small model
// spends its limited attention on whatever it is shown.
func Sessionise(events []event.Event) []Session {
	var kept []event.Event
	for _, e := range events {
		if e.Kind == event.Focus && e.DurS < event.IncidentalSecs {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return nil
	}

	sort.Slice(kept, func(i, j int) bool { return kept[i].TS < kept[j].TS })

	var out []Session
	cur := Session{Start: kept[0].TS, End: kept[0].TS + kept[0].DurS}

	for _, e := range kept {
		end := e.TS + e.DurS
		if len(cur.Events) > 0 && e.TS-cur.End > IdleGapS {
			out = append(out, cur)
			cur = Session{Start: e.TS, End: end}
		}
		cur.Events = append(cur.Events, e)
		cur.EventIDs = append(cur.EventIDs, e.ID)
		if end > cur.End {
			cur.End = end
		}
	}
	return append(out, cur)
}

// Digest renders a session for a model: compact, deduplicated, and capped.
//
// Feeding 400 near-identical rows to an 8B model wastes context and degrades
// the output. What matters is which apps, which pages, which commits — not the
// exact ordering of every tab switch.
func (s Session) Digest(maxItems int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Session %s–%s (%dm)\n",
		hhmm(s.Start), hhmm(s.End), s.DurS()/60)

	if apps := s.Apps(); len(apps) > 0 {
		fmt.Fprintf(&b, "Apps: %s\n", strings.Join(apps, ", "))
	}

	seen := map[string]bool{}
	var urls, commits, titles []string

	for _, e := range s.Events {
		switch e.Kind {
		case event.URL:
			if h := host(e.URL); h != "" && !seen["u"+h] {
				seen["u"+h] = true
				urls = append(urls, h)
			}
		case event.Commit:
			if !seen["c"+e.Title] {
				seen["c"+e.Title] = true
				commits = append(commits, fmt.Sprintf("%s: %s", e.Path, e.Title))
			}
		case event.Focus:
			if e.Title != "" && !seen["t"+e.Title] {
				seen["t"+e.Title] = true
				titles = append(titles, e.Title)
			}
		}
	}

	writeCapped(&b, "Windows", titles, maxItems)
	writeCapped(&b, "Sites", urls, maxItems)
	writeCapped(&b, "Commits", commits, maxItems)
	return b.String()
}

// DigestForEntities is Digest minus the host list.
//
// Showing a small model a list of visited domains reliably produces a list of
// domains back, regardless of how the prompt is worded. The entities actually
// worth a note come from window titles and commit subjects — text the user or
// their collaborators wrote — so that is all this carries.
func (s Session) DigestForEntities(maxItems int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Session %s–%s (%dm)\n", hhmm(s.Start), hhmm(s.End), s.DurS()/60)

	seen := map[string]bool{}
	var commits, titles []string

	for _, e := range s.Events {
		switch e.Kind {
		case event.Commit:
			if !seen["c"+e.Title] {
				seen["c"+e.Title] = true
				commits = append(commits, fmt.Sprintf("%s: %s", e.Path, e.Title))
			}
		case event.Focus:
			if e.Title != "" && !seen["t"+e.Title] {
				seen["t"+e.Title] = true
				titles = append(titles, e.Title)
			}
		}
	}

	writeCapped(&b, "Windows", titles, maxItems)
	writeCapped(&b, "Commits", commits, maxItems)
	return b.String()
}

func writeCapped(b *strings.Builder, label string, items []string, maxItems int) {
	if len(items) == 0 {
		return
	}
	shown := items
	suffix := ""
	if len(items) > maxItems {
		shown = items[:maxItems]
		suffix = fmt.Sprintf(" (+%d more)", len(items)-maxItems)
	}
	fmt.Fprintf(b, "%s: %s%s\n", label, strings.Join(shown, "; "), suffix)
}

func host(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}
