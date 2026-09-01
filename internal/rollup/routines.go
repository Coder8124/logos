package rollup

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/routine"
)

// Naming is the model's only job here. Mining found the pattern; the model
// writes it down in a sentence a person would recognise. If the model is
// unavailable the routine is still perfectly usable — it just gets a plainer
// description — so naming must never be able to block a proposal.

var nameSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"description": map[string]any{"type": "string"},
	},
	"required":             []string{"description"},
	"additionalProperties": false,
}

// Propose turns mined patterns into queue entries.
//
// Routines go through the same review queue as everything else — there is no
// privileged write path. A discovered pattern is still an inference about the
// user, and inferences get approved.
func ProposeRoutines(db *sql.DB, rt *router.Router, periodics []routine.Periodic, sequences []routine.Sequence) (int, error) {
	x := newNamer(rt)
	queued := 0

	for _, p := range periodics {
		desc := fmt.Sprintf("Opens %s on %s, usually %s (%d times over %d weeks, %.0f%% of days).",
			p.App, p.Cadence(), p.Window(), p.Occurrences, p.Weeks, p.Consistency*100)

		if named := x.name(desc); named != "" {
			desc = named
		}

		prop := &Proposal{
			Kind:     NewNote,
			Target:   p.Slug(),
			Conf:     confidenceOf(p),
			Evidence: p.EventIDs,
			Model:    x.model,
			Payload: Payload{
				Title: fmt.Sprintf("%s on %s", p.App, p.Cadence()),
				Type:  "routine",
				Body:  desc,
			},
		}
		if err := Enqueue(db, prop); err != nil {
			return queued, err
		}
		queued++
	}

	for _, s := range sequences {
		desc := fmt.Sprintf("%s is usually followed by %s (%d times, %.0f%% of the time).",
			s.From, s.To, s.Count, s.Share*100)

		prop := &Proposal{
			Kind:     NewNote,
			Target:   fmt.Sprintf("routines/%s-then-%s", slugify(s.From), slugify(s.To)),
			Conf:     min(0.85, 0.4+s.Share/2),
			Evidence: s.EventIDs,
			Model:    "mining",
			Payload: Payload{
				Title: s.String(),
				Type:  "routine",
				Body:  desc,
			},
		}
		if err := Enqueue(db, prop); err != nil {
			return queued, err
		}
		queued++
	}

	return queued, nil
}

// confidenceOf scores a routine by how much it repeated and how tightly.
// Consistency dominates: a pattern that holds four days in five is a routine
// even if the window is loose, while a tight window seen rarely is a habit.
func confidenceOf(p routine.Periodic) float64 {
	conf := 0.3 + p.Consistency*0.5
	if p.Weeks >= 6 {
		conf += 0.1
	}
	if p.SpreadS < 15*60 {
		conf += 0.05
	}
	return max(0.05, min(0.9, conf))
}

type namer struct {
	rt    *router.Router
	model string
	ok    bool
}

func newNamer(rt *router.Router) *namer {
	n := &namer{rt: rt, model: "mining"}
	if m, err := rt.ModelFor(router.T1, true); err == nil {
		n.model, n.ok = m, true
	}
	return n
}

func (n *namer) name(stat string) string {
	if !n.ok {
		return ""
	}

	const system = "Rewrite one observed usage statistic as a single plain sentence a person " +
		"would recognise about their own week. Reply with JSON only. Keep every number " +
		"exactly as given. Do not speculate about why, do not add advice, do not " +
		"mention productivity."

	out, err := n.rt.Local().Chat(n.model, system, stat, nameSchema)
	if err != nil {
		return "" // naming is cosmetic; never let it block the proposal
	}

	var res struct {
		Description string `json:"description"`
	}
	if json.Unmarshal([]byte(cleanJSON(out)), &res) != nil || res.Description == "" {
		return ""
	}
	return res.Description
}

func slugify(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
		case r == ' ' || r == '-' || r == '_':
			out = append(out, '-')
		}
	}
	return string(out)
}
