package contextpack

import (
	"fmt"

	"github.com/Coder8124/logos/internal/session"
)

// RulingCost is what the latest checkpoint's ruled-out approaches took to
// learn, counted from records that already exist. A zero is "the record does
// not say", never "it cost nothing", so the render says nothing rather than
// print a zero that reads as a measurement.
//
// There is deliberately no count of failed commands. The activity log holds
// every failing shell command of a session and nothing that says which were
// the dead end: a typo, a flaky test and the ruled-out approach fail alike, so
// any such number would be one nobody measured.
type RulingCost struct {
	// EarlierSessions is the earlier conversations in this repository whose
	// checkpoints recorded one of the same rulings: each is a session that
	// walked into the dead end before this one did. Commit carries no ruling
	// forward on its own, so a repeat is an agent arriving there again, not a
	// copy.
	EarlierSessions int
}

// loadRulingCost fills p.Cost from earlier, the history Build already read
// and filtered to this repository. "Carrying 1 ruled-out approach" counts what
// was kept and says nothing about what keeping it saves, which is the one
// thing that makes the restore worth reading; this is that, from the vault's
// own files, so deleting the index changes nothing.
func (p *Pack) loadRulingCost(earlier []session.Checkpoint) {
	c := p.Checkpoint
	if c == nil {
		return
	}
	keys := map[string]bool{}
	for _, f := range c.Failed {
		if !session.IsPlaceholder(f) {
			keys[normalizeKey(f)] = true
		}
	}
	if len(keys) == 0 {
		return
	}
	seen := map[string]bool{}
	n := 0
	for _, old := range earlier {
		// The same sitting checkpointed twice is one session, not a
		// predecessor; without a host id the checkpoint is its own.
		id := old.HostSession
		if id == "" {
			id = old.Slug
		}
		if old.Slug == c.Slug || (c.HostSession != "" && id == c.HostSession) || seen[id] {
			continue
		}
		for _, f := range old.Failed {
			if keys[normalizeKey(f)] {
				seen[id] = true
				n++
				break
			}
		}
	}
	if n > 0 {
		p.Cost = &RulingCost{EarlierSessions: n}
	}
}

// costLine renders p.Cost as the one sentence the SessionStart hook lifts into
// its banner. It is written above the first heading, where every line is one
// the renderer built from single-line values, and the hook reads it from
// nowhere else: a checkpoint quoting the sentence in its State lands under a
// heading, so it cannot put a cost on the banner nobody counted.
//
// The count includes the session that ruled it out and the one about to
// re-derive it, because "the 4th time" is the claim the reader can weigh;
// "2 earlier sessions" leaves them to do the arithmetic.
func (p *Pack) costLine() string {
	if p.Cost == nil || p.Checkpoint == nil {
		return ""
	}
	n := 0
	for _, f := range p.Checkpoint.Failed {
		if !session.IsPlaceholder(f) {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("Re-deriving the ruled-out %s below would be the %s time a session hit %s.",
		plural(n, "approach", "approaches"), ordinal(p.Cost.EarlierSessions+2), plural(n, "this dead end", "one of these dead ends"))
}

// ordinal is 1st, 2nd, 3rd, 4th, and 11th through 13th.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
