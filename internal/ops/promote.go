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
	Superseded int // promoted decisions a later accepted one reversed
}

// decided is one decision as a checkpoint recorded it, keyed and tokenised
// once per pass rather than once per comparison.
type decided struct {
	key, text string
	subject   map[string]bool
}

// held is a decision across the window: the newest wording, the checkpoints
// that recorded it newest first, and the oldest and newest of their times.
type held struct {
	decided
	slugs       []string
	when        []int64         // each slug's checkpoint time
	conv        []string        // each slug's host conversation, "" when the host named none
	in          map[string]bool // slugs as a set: were two ever recorded together
	first, last int64
}

// PromoteDecisions turns the decisions that hold on a project into proposed
// project memory, and supersedes a promoted one that a later promoted one
// reversed.
//
// A decision lived only in checkpoints, and resume reads a handful of those:
// the project's settled questions fell out of every pack as soon as enough
// checkpoints had landed without restating them. A memory does not age out of
// a window. Proposed, not stored: the review queue is where a machine's
// conclusion waits for a person, or for the review switch — and that holds for
// a reversal too, which is proposed like any decision and replaces the old
// memory only once it is accepted.
//
// Everything it reads is markdown — the checkpoints, and the memory timeline
// log.md restores — so a rebuilt index proposes nothing twice.
func PromoteDecisions(db *sql.DB, vaultDir, project string) (Promotion, error) {
	var done Promotion
	hist, err := session.History(vaultDir, project, session.DecisionWindow)
	if err != nil || len(hist) == 0 {
		return done, err
	}
	seen, err := memory.SeenTexts(db)
	if err != nil {
		return done, err
	}
	// Met by its key, not its exact text: the proposal is worded as the newest
	// checkpoint words it, and a rejected "X" must not come back as "X." the
	// next time an agent adds a full stop.
	met := map[string]bool{}
	for t := range seen {
		met[session.DecisionKey(t)] = true
	}

	per, order, byKey := heldIn(hist)

	var failed []error
	for _, k := range order {
		h := byKey[k]
		if len(h.slugs) < DecisionHolds || met[k] || reversedAfter(hist, per, h) {
			continue
		}
		text := decisionWording(h.text)
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
	n, err := supersedeReversed(db, project, byKey)
	done.Superseded = n
	return done, errors.Join(append(failed, err)...)
}

// heldIn groups a history's decisions by key: each checkpoint's decisions, the
// keys in the order they were first met newest first, and each key's held.
// Shared with the ledger, so what it strikes through as reversed and what this
// pass supersedes are one reading of the same checkpoints.
func heldIn(hist []session.Checkpoint) (per [][]decided, order []string, byKey map[string]*held) {
	per = make([][]decided, len(hist))
	byKey = map[string]*held{}
	for i, c := range hist { // newest first
		per[i] = decisionsOf(c)
		for _, d := range per[i] {
			h := byKey[d.key]
			if h == nil {
				h = &held{decided: d, in: map[string]bool{}, last: c.TS}
				byKey[d.key] = h
				order = append(order, d.key)
			}
			h.slugs = append(h.slugs, c.Slug)
			h.when = append(h.when, c.TS)
			h.conv = append(h.conv, c.HostSession)
			h.in[c.Slug] = true
			h.first = c.TS
		}
	}
	return per, order, byKey
}

// replaces is whether r reversed old: the same question, never recorded in
// one checkpoint together, said last, and taken up again after old was last
// said (takenUp).
func replaces(old, r *held) bool { return takenUp(old, r) >= 0 }

// takenUp is the index in r.slugs of the oldest checkpoint at which r took
// the question up after old was last recorded, and -1 when r never did — when
// r is not a reversal of old at all.
//
// Never together is what tells a reversal from a neighbour: two decisions on
// one subject that agents copy forward together are both standing. Beyond
// that, a recording of r after old's last counts only when it is a take-up,
// not r carrying on beside old:
//
//   - r was first said after old's last: nothing recorded r while old stood.
//   - its conversation recorded old earlier: that sitting held old, then
//     moved to r. A reinstatement in one sitting, Redis, Postgres, Redis.
//   - its conversation never recorded r while old stood: a later sitting
//     took r up again after old was said, a reinstatement across sittings.
//
// Timestamps alone cannot tell a reinstatement from two conversations running
// side by side — A at t1..t5 in one, B at t3 in another has the same shape as
// Redis t1, Postgres t2, Redis t3 — and reading the parallel case as a
// reversal had promotion Correct one standing decision into the other. So
// the last two need the host's conversation id; a checkpoint whose host named
// none is only ever a take-up by the first.
func takenUp(old, r *held) int {
	if r.last <= old.last || textmatch.Overlap(old.subject, r.subject) < textmatch.Related {
		return -1
	}
	a, b := old.in, r.in
	if len(b) < len(a) {
		a, b = b, a
	}
	for s := range a {
		if b[s] {
			return -1
		}
	}
	fresh := r.first > old.last
	sawOld := map[string]bool{}
	for _, c := range old.conv {
		sawOld[c] = true
	}
	heldR := map[string]bool{} // conversations recording r while old stood
	for i, t := range r.when {
		if t <= old.last {
			heldR[r.conv[i]] = true
		}
	}
	at := -1
	for i, t := range r.when { // newest first, so the last match is the oldest
		if t <= old.last {
			continue
		}
		if c := r.conv[i]; fresh || (c != "" && (sawOld[c] || !heldR[c])) {
			at = i
		}
	}
	return at
}

// saidLater is which of two replacements of one decision stands: the one said
// last, ties to the one first said later and then to the key, so the choice
// never depends on map order.
func saidLater(r, by *held) bool {
	if r.last != by.last {
		return r.last > by.last
	}
	if r.first != by.first {
		return r.first > by.first
	}
	return r.key > by.key
}

// supersessions is, for each of keys that a later decision replaced, the one
// that replaced it last.
func supersessions(keys []string, byKey map[string]*held) map[string]*held {
	akin := newAkinIndex(byKey)
	out := map[string]*held{}
	for _, k := range keys {
		h := byKey[k]
		var by *held
		for _, r := range akin.candidates(h) {
			if replaces(h, r) && (by == nil || saidLater(r, by)) {
				by = r
			}
		}
		if by != nil {
			out[k] = by
		}
	}
	return out
}

// akinIndex files each decision under the stems of its subject's words
// (textmatch.AkinStem). A decision with no akin word in common with another
// overlaps it by nothing, so only decisions sharing a stem are ever compared:
// comparing every pair was quadratic in a history that only grows.
type akinIndex map[string][]*held

func newAkinIndex(byKey map[string]*held) akinIndex {
	ix := akinIndex{}
	for _, h := range byKey {
		for w := range h.subject {
			st := textmatch.AkinStem(w)
			ix[st] = append(ix[st], h)
		}
	}
	return ix
}

// candidates is every other decision sharing a stem with h, once each.
func (ix akinIndex) candidates(h *held) []*held {
	var out []*held
	seen := map[*held]bool{h: true}
	for w := range h.subject {
		for _, r := range ix[textmatch.AkinStem(w)] {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// decisionsOf is a checkpoint's decisions, once each by key.
func decisionsOf(c session.Checkpoint) []decided {
	var out []decided
	in := map[string]bool{}
	for _, d := range c.Decisions {
		k := session.DecisionKey(d)
		// A checkpoint written before placeholders were dropped still says
		// "None this session" on disk, and it is not a decision anybody made.
		if k == "" || in[k] || session.IsPlaceholder(d) {
			continue
		}
		in[k] = true
		out = append(out, decided{key: k, text: strings.Join(strings.Fields(d), " "), subject: textmatch.Subject(d)})
	}
	return out
}

// decisionWording is the text a promoted decision is stored under: worded as
// the review switch recognises a decision.
func decisionWording(text string) string {
	if t := strings.ToLower(text); strings.HasPrefix(t, "decided") || strings.HasPrefix(t, "decision:") {
		return text
	}
	return "Decision: " + text
}

// reversedAfter is whether a checkpoint newer than the last that recorded h
// decided the same question differently — the test resume's "Decided earlier"
// already uses, so what is promoted and what resume hands over agree.
func reversedAfter(hist []session.Checkpoint, per [][]decided, h *held) bool {
	for i, c := range hist {
		if c.TS <= h.last {
			continue
		}
		for _, d := range per[i] {
			if d.key != h.key && textmatch.Overlap(h.subject, d.subject) >= textmatch.Related {
				return true
			}
		}
	}
	return false
}

// supersedeReversed replaces a promoted decision with a later promoted one on
// the same question that the checkpoints took up again after they last
// recorded the first (replaces). Both have to be standing memory, so the
// reversal went through review like the decision it replaces. And "took up"
// is what tells a reversal from a neighbour or a parallel conversation: two
// decisions agents copy forward together, or two sittings each carrying their
// own, are both standing, and neither replaces the other.
//
// Through Correct, which folds the old memory into the reversal with its uid
// named, never edited in place: what the project used to hold stays on record
// as what it used to hold.
func supersedeReversed(db *sql.DB, project string, byKey map[string]*held) (int, error) {
	mems, err := memory.AllInProject(db, project)
	if err != nil {
		return 0, err
	}
	type promoted struct {
		m memory.Memory
		h *held
	}
	var ps []promoted
	for _, m := range mems {
		if m.Source != memory.SourceCheckpoints || m.Project != project {
			continue
		}
		if h := byKey[session.DecisionKey(m.Text)]; h != nil {
			ps = append(ps, promoted{m, h})
		}
	}
	var n int
	var failed []error
	gone := map[int64]bool{}
	for _, old := range ps {
		var by *promoted
		for i, r := range ps {
			if r.m.ID == old.m.ID || gone[r.m.ID] || !replaces(old.h, r.h) {
				continue
			}
			if by == nil || saidLater(r.h, by.h) {
				by = &ps[i]
			}
		}
		if by == nil {
			continue
		}
		c, err := memory.Correct(db, nil, "", old.m.ID, &memory.Memory{Text: by.m.Text, Source: memory.SourceCheckpoints})
		if err != nil {
			failed = append(failed, fmt.Errorf("could not supersede memory #%d: %w", old.m.ID, err))
			continue
		}
		if c.ID != 0 {
			gone[old.m.ID] = true
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
		said = append(said, fmt.Sprintf("%d promoted %s superseded by a later one that reversed it",
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
