package ops

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
)

// DecisionHolds is how many checkpoints have to record a decision before it is
// proposed as project memory. Once is a choice; agents copying it forward into
// a third checkpoint is the project treating it as settled.
const DecisionHolds = 3

// checkpointsRef is how a promoted decision cites the checkpoints it came
// from, in the Turn field a transcript-made proposal cites its turn in: the
// queue file already carries that field through a rebuild, and the review
// switch already decides by it.
const checkpointsRef = "checkpoints:"

// Promotion is what one PromoteDecisions pass did.
type Promotion struct {
	Proposed   int // decisions put in the review queue as project memory
	Superseded int // promoted decisions a later checkpoint reversed
}

// PromoteDecisions turns the decisions that hold on a project into proposed
// project memory, and supersedes a promoted one a later checkpoint reversed.
//
// A decision lived only in checkpoints, and resume reads a handful of those:
// the project's settled questions fell out of every pack as soon as enough
// checkpoints had landed without restating them. A memory does not age out of
// a window. Proposed, not stored: the review queue is where a machine's
// conclusion waits for a person, or for the review switch.
//
// Everything it reads is markdown — the checkpoints, and the memory timeline
// log.md restores — so a rebuilt index proposes nothing twice.
func PromoteDecisions(db *sql.DB, vaultDir, project string) (Promotion, error) {
	var done Promotion
	hist, err := session.History(vaultDir, project, 0)
	if err != nil || len(hist) == 0 {
		return done, err
	}
	seen, err := memory.SeenTexts(db)
	if err != nil {
		return done, err
	}

	type held struct {
		text    string   // the newest wording
		ts      int64    // the newest checkpoint that recorded it
		slugs   []string // every checkpoint that recorded it, newest first
		subject map[string]bool
	}
	var order []string
	byKey := map[string]*held{}
	for _, c := range hist { // newest first
		inThis := map[string]bool{}
		for _, d := range c.Decisions {
			k := session.DecisionKey(d)
			if k == "" || inThis[k] {
				continue
			}
			inThis[k] = true
			h := byKey[k]
			if h == nil {
				h = &held{text: strings.Join(strings.Fields(d), " "), ts: c.TS, subject: textmatch.Subject(d)}
				byKey[k] = h
				order = append(order, k)
			}
			h.slugs = append(h.slugs, c.Slug)
		}
	}

	var failed []error
	for _, k := range order {
		h := byKey[k]
		if len(h.slugs) < DecisionHolds || reversedAfter(hist, k, h.subject, h.ts) {
			continue
		}
		text := h.text
		if t := strings.ToLower(text); !strings.HasPrefix(t, "decided") && !strings.HasPrefix(t, "decision:") {
			// Worded as the review switch recognises a decision.
			text = "Decision: " + text
		}
		// Proposed, accepted, rejected or forgotten, it has been met: a person
		// who threw the proposal out is not asked again at every checkpoint.
		if seen[text] {
			continue
		}
		m := memory.Memory{
			Text: text, Kind: memory.Context, Project: project, Source: memory.SourceCheckpoints,
			Salience: 0.7, Quarantined: true, Turn: checkpointsRef + strings.Join(h.slugs, ","),
		}
		r, err := memory.Store(db, nil, "", &m)
		if err != nil {
			failed = append(failed, fmt.Errorf("could not propose %q: %w", text, err))
			continue
		}
		if r.Outcome == memory.EvQuarantined {
			done.Proposed++
		}
	}

	n, err := supersedeReversed(db, project, hist)
	done.Superseded = n
	return done, errors.Join(append(failed, err)...)
}

// reversedAfter is whether a checkpoint newer than ts decided the same
// question differently — the test resume's "Decided earlier" already uses, so
// what is promoted and what resume hands over agree.
func reversedAfter(hist []session.Checkpoint, key string, subject map[string]bool, ts int64) bool {
	for _, c := range hist {
		if c.TS <= ts {
			continue
		}
		for _, d := range c.Decisions {
			if session.DecisionKey(d) != key && textmatch.Overlap(subject, textmatch.Subject(d)) >= textmatch.Related {
				return true
			}
		}
	}
	return false
}

// supersedeReversed replaces a promoted decision that a checkpoint written
// after it decided the other way. Through Correct, so the old memory is
// superseded with its uid named, never edited in place: what the project used
// to hold stays on record as what it used to hold.
func supersedeReversed(db *sql.DB, project string, hist []session.Checkpoint) (int, error) {
	mems, err := memory.AllInProject(db, project)
	if err != nil {
		return 0, err
	}
	var n int
	var failed []error
	for _, m := range mems {
		if m.Source != memory.SourceCheckpoints || m.Project != project {
			continue
		}
		key, subject := session.DecisionKey(m.Text), textmatch.Subject(m.Text)
		var by string
		for i := len(hist) - 1; i >= 0 && by == ""; i-- { // oldest first: the first reversal
			if hist[i].TS <= m.Created {
				continue
			}
			for _, d := range hist[i].Decisions {
				if session.DecisionKey(d) != key && textmatch.Overlap(subject, textmatch.Subject(d)) >= textmatch.Related {
					by = strings.Join(strings.Fields(d), " ")
					break
				}
			}
		}
		if by == "" {
			continue
		}
		if t := strings.ToLower(by); !strings.HasPrefix(t, "decided") && !strings.HasPrefix(t, "decision:") {
			by = "Decision: " + by
		}
		c, err := memory.Correct(db, nil, "", m.ID, &memory.Memory{Text: by, Source: memory.SourceCheckpoints})
		if err != nil {
			failed = append(failed, fmt.Errorf("could not supersede memory #%d: %w", m.ID, err))
		}
		if c.ID != 0 {
			n++
		}
	}
	return n, errors.Join(failed...)
}

// PromotionReceipt is the line a checkpoint adds when the pass did something,
// "" when it did nothing.
func PromotionReceipt(p Promotion) string {
	var said []string
	if p.Proposed > 0 {
		said = append(said, fmt.Sprintf("%d %s that held across %d checkpoints proposed as project memory (`logos review`)",
			p.Proposed, plural(p.Proposed, "decision", "decisions"), DecisionHolds))
	}
	if p.Superseded > 0 {
		said = append(said, fmt.Sprintf("%d promoted %s reversed by a later checkpoint and superseded",
			p.Superseded, plural(p.Superseded, "decision", "decisions")))
	}
	if len(said) == 0 {
		return ""
	}
	return strings.Join(said, "; ") + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
