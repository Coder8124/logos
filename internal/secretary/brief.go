package secretary

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/routine"
)

// A Brief is what the secretary says when you open it: the proactive digest
// that makes this a secretary and not an archive. It is assembled, not asked
// for.
//
// Every line is arithmetic over data already captured — no model runs to build
// a brief, so it is instant and it works offline. The model's only role is the
// upstream extraction of commitments; surfacing them is deterministic.
type Brief struct {
	Greeting  string    `json:"greeting"`
	Upcoming  []Meeting `json:"upcoming"`
	Loops     []Loop    `json:"loops"`
	Dormant   []Nudge   `json:"dormant"`
	Usual     []Nudge   `json:"usual"`
	Remembers []string  `json:"remembers"` // standing preferences/context worth keeping in mind
	Review    int       `json:"review"`
}

// Meeting is a calendar event coming up soon. The most time-sensitive thing a
// secretary surfaces — everything else can wait, a meeting cannot.
type Meeting struct {
	Title    string `json:"title"`
	Cal      string `json:"cal"`
	At       string `json:"at"`       // "14:30"
	InMin    int    `json:"in_min"`   // minutes from now
	Imminent bool   `json:"imminent"` // within 15 minutes
}

type Loop struct {
	ID      int64  `json:"id"`
	Text    string `json:"text"`
	Who     string `json:"who"`
	AgeDays int    `json:"age_days"`
	Due     string `json:"due"`
	// Stale marks a loop old enough to lead with — the thing most likely to
	// have fallen through a crack.
	Stale bool `json:"stale"`
}

type Nudge struct {
	Text   string `json:"text"`
	Detail string `json:"detail"`
}

// Compose builds the briefing for a given moment. now is passed in rather than
// read from the clock so the whole thing is testable.
func Compose(db *sql.DB, now time.Time) (Brief, error) {
	var b Brief
	b.Greeting = greeting(now)
	memory.Init(db)

	// --- what's coming up: the most time-sensitive thing, so it leads ---
	// Read from the store, not live from EventKit, so the brief stays instant
	// and works offline; the capture poll keeps the window fresh.
	cal, err := capture.Range(db, now.Unix(), now.Add(8*time.Hour).Unix())
	if err != nil {
		return b, err
	}
	for _, e := range cal {
		if e.Kind != capture.Calendar {
			continue
		}
		mins := int(time.Unix(e.TS, 0).Sub(now).Minutes())
		if mins < 0 {
			continue
		}
		b.Upcoming = append(b.Upcoming, Meeting{
			Title:    e.Title,
			Cal:      e.App,
			At:       time.Unix(e.TS, 0).Local().Format("15:04"),
			InMin:    mins,
			Imminent: mins <= 15,
		})
		if len(b.Upcoming) >= 4 {
			break
		}
	}

	// --- open loops, stalest first ---
	loops, err := Open_(db)
	if err != nil {
		return b, err
	}
	sort.Slice(loops, func(i, j int) bool { return loops[i].Created < loops[j].Created })
	for _, c := range loops {
		age := int(now.Sub(time.Unix(c.Created, 0)).Hours() / 24)
		b.Loops = append(b.Loops, Loop{
			ID: c.ID, Text: c.Text, Who: c.Who, AgeDays: age, Due: c.DueHint,
			// Three days with no movement is when a loop stops being "recent"
			// and starts being "forgotten".
			Stale: age >= 3,
		})
	}

	// --- what has gone quiet, and what you usually do around now ---
	// A wide window so the baseline is stable; both signals are pure counts.
	events, err := capture.Range(db, now.AddDate(0, 0, -400).Unix(), now.Unix()+1)
	if err != nil {
		return b, err
	}

	for _, a := range routine.FindAnomalies(events, now.Unix()) {
		b.Dormant = append(b.Dormant, Nudge{
			Text:   fmt.Sprintf("%s has gone quiet", a.App),
			Detail: fmt.Sprintf("%.0f days since last use, usually every %.1f", a.ActualGapD, a.TypicalGapD),
		})
		if len(b.Dormant) >= 3 {
			break
		}
	}

	for _, n := range usualNow(events, now) {
		b.Usual = append(b.Usual, n)
		if len(b.Usual) >= 3 {
			break
		}
	}

	// --- standing preferences and context the assistant keeps in mind ---
	// This is what makes the brief feel like it knows you: the high-salience
	// things it has learned, surfaced without being asked. The "context
	// management" a good secretary has, from persistent memory.
	if mems, err := memory.Surface(db, []memory.Kind{memory.Preference, memory.Context}, 3); err == nil {
		for _, m := range mems {
			b.Remembers = append(b.Remembers, m.Text)
		}
	}

	return b, nil
}

// usualNow finds routines whose typical window contains the current time, so
// the brief can say "you normally have X open around now". This is the
// anticipatory half of a secretary — not "what did you do" but "what comes
// next".
func usualNow(events []capture.Event, now time.Time) []Nudge {
	nowOffset := int64(now.Hour()*3600 + now.Minute()*60)
	weekday := now.Weekday() != time.Saturday && now.Weekday() != time.Sunday

	// Consider both app and site routines; a morning that always starts on a
	// particular site is as much a routine as one that starts in an app.
	var candidates []routine.Periodic
	candidates = append(candidates, routine.FindPeriodic(events)...)
	candidates = append(candidates, routine.FindPeriodicSites(events)...)

	var out []Nudge
	seen := map[string]bool{}
	for _, p := range candidates {
		if p.Weekday != weekday || seen[p.App] {
			continue
		}
		// Within the routine's own spread, widened slightly so a brief opened a
		// few minutes early still anticipates the pattern.
		lo := p.MedianStart - p.SpreadS - 15*60
		hi := p.MedianStart + p.SpreadS + 15*60
		if nowOffset >= lo && nowOffset <= hi {
			seen[p.App] = true
			out = append(out, Nudge{
				Text:   fmt.Sprintf("you usually have %s open around now", p.App),
				Detail: fmt.Sprintf("%s on %s, %.0f%% of days", p.Window(), p.Cadence(), p.Consistency*100),
			})
		}
	}
	return out
}

func greeting(now time.Time) string {
	switch h := now.Hour(); {
	case h < 5:
		return "Still up"
	case h < 12:
		return "Morning"
	case h < 17:
		return "Afternoon"
	case h < 22:
		return "Evening"
	default:
		return "Late one"
	}
}

// Headline is the single most important thing to say, for a collapsed orb
// tooltip or a one-line summary. A secretary leads with the point — and an
// imminent meeting outranks everything, because it is the only item with a
// hard deadline you cannot recover from missing.
func (b Brief) Headline() string {
	if len(b.Upcoming) > 0 {
		m := b.Upcoming[0]
		if m.Imminent {
			return fmt.Sprintf("%s in %dm", m.Title, m.InMin)
		}
	}
	for _, l := range b.Loops {
		if l.Stale {
			who := ""
			if l.Who != "" {
				who = " (" + l.Who + ")"
			}
			return fmt.Sprintf("%dd open: %s%s", l.AgeDays, l.Text, who)
		}
	}
	if len(b.Loops) > 0 {
		return fmt.Sprintf("%d open loop(s)", len(b.Loops))
	}
	if len(b.Dormant) > 0 {
		return b.Dormant[0].Text
	}
	if b.Review > 0 {
		return fmt.Sprintf("%d proposals to review", b.Review)
	}
	return "nothing pressing"
}

// IsQuiet reports whether the brief has nothing worth interrupting for, so the
// app can stay out of the way rather than manufacturing noise.
func (b Brief) IsQuiet() bool {
	return len(b.Upcoming) == 0 && len(b.Loops) == 0 &&
		len(b.Dormant) == 0 && len(b.Usual) == 0 && b.Review == 0
}
