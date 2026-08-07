// Package presence is the Secretary as a presence — the ambient, conversational
// layer that speaks up about your calendar, your agenda, and the things you meant
// to remember but never quite do.
//
// It is bound by one law: augment, never override. It proposes and reminds; it
// never decides, never acts outward without the confirmation gate, and never
// rewrites a conclusion you've reached. If a behaviour can't be phrased as "here's
// what I noticed, over to you," it does not belong here.
//
// Selection is arithmetic over signals other subsystems already compute (the
// brief's meetings and loops, routine anomalies, dream insights); the model only
// phrases what this package decides to raise. Nothing is spoken twice, non-urgent
// nudges are spaced apart, and only an imminent meeting may break your focus.
package presence

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/pragun/brain/internal/dream"
	"github.com/pragun/brain/internal/secretary"
)

// Kind is which of the three things a nudge is about.
type Kind string

const (
	Calendar Kind = "calendar" // an upcoming meeting
	Agenda   Kind = "agenda"   // an open commitment due or gone stale
	Remember Kind = "remember" // something you meant to keep in mind
)

// Interjection is one thing the presence might say. Key is a stable fingerprint
// so the same thing is never raised twice.
type Interjection struct {
	Kind     Kind   `json:"kind"`
	Text     string `json:"text"`     // what it says
	Detail   string `json:"detail"`   // the evidence behind it — cited, never editorial
	Critical bool   `json:"critical"` // may interrupt focus (an imminent meeting)
	Key      string `json:"key"`
}

// Check composes the candidate interjections for a moment, most-urgent first. It
// does not consult the cooldown, quiet hours, or what's already been said — that
// is Next's job; Check is the pure "what could be raised right now".
func Check(db *sql.DB, now time.Time, leadMinutes int) ([]Interjection, error) {
	b, err := secretary.Compose(db, now)
	if err != nil {
		return nil, err
	}

	var out []Interjection

	// Calendar — the one class allowed to break focus.
	for _, m := range b.Upcoming {
		if m.InMin > leadMinutes {
			continue
		}
		when := fmt.Sprintf("in %d minutes", m.InMin)
		if m.InMin <= 1 {
			when = "now"
		}
		out = append(out, Interjection{
			Kind: Calendar, Critical: true,
			Text:   fmt.Sprintf("%s — %s.", m.Title, when),
			Detail: fmt.Sprintf("at %s%s", m.At, cal(m.Cal)),
			Key:    fmt.Sprintf("cal:%s@%s", m.Title, m.At),
		})
	}

	// Agenda — an open loop that's due or has gone stale. Raised, not nagged.
	for _, l := range b.Loops {
		if !l.Stale && l.Due == "" {
			continue
		}
		out = append(out, Interjection{
			Kind:   Agenda,
			Text:   fmt.Sprintf("You still mean to: %s", l.Text),
			Detail: loopDetail(l),
			Key:    fmt.Sprintf("loop:%d", l.ID),
		})
	}

	// Remember — dreamed connections waiting, then work that's gone quiet.
	if ins, err := dream.List(db, dream.Pending); err == nil {
		for _, in := range ins {
			out = append(out, Interjection{
				Kind:   Remember,
				Text:   "Something occurred to me overnight: " + in.Text,
				Detail: "a dreamed connection — `brain dream review` to keep or drop it",
				Key:    fmt.Sprintf("insight:%d", in.ID),
			})
		}
	}
	for _, n := range b.Dormant {
		out = append(out, Interjection{
			Kind:   Remember,
			Text:   n.Text,
			Detail: n.Detail,
			Key:    "dormant:" + n.Text,
		})
	}

	// Critical first; within a class, preserve the brief's own ordering
	// (stalest / soonest first).
	sort.SliceStable(out, func(i, j int) bool { return out[i].Critical && !out[j].Critical })
	return out, nil
}

func cal(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

func loopDetail(l secretary.Loop) string {
	switch {
	case l.Due != "":
		return "due " + l.Due
	case l.AgeDays == 1:
		return "open since yesterday"
	default:
		return fmt.Sprintf("open %d days", l.AgeDays)
	}
}
