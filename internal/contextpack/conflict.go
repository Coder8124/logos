package contextpack

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/textmatch"
)

// Two facts about the same thing that do not agree.
//
// Every memory system benchmarked handles this the same way: it returns both
// and says nothing. Asked "what is the retail price?" against a store holding
// $199, then $229, then $249, they hand over all three undifferentiated, and
// the agent picks one — often the first, which is the oldest. That is worse
// than returning nothing, because it is confidently wrong and cited.
//
// The distinction that matters is between two shapes of disagreement:
//
//   - **Supersession.** The same claim restated over time as it changed. The
//     newest is true and the older ones are history. Return the newest; keep
//     the rest out of the way.
//   - **Contradiction.** Two sources that disagree with no ordering between
//     them — a summary saying 71% and a factory report saying 63%. Neither is
//     obviously right, and picking one silently is the failure. Return both,
//     and say they disagree.
//
// Both are found the same way: statements about the same subject carrying
// different values. This is a heuristic, not comprehension. It is tuned to be
// quiet — it requires strong topical overlap *and* a genuine value difference —
// because a false supersession silently deletes a true fact, which is the one
// outcome worse than the problem it fixes.

// disagree reports whether two statements are about the same thing and assert
// different values for it.
//
// Both halves are required. Same subject with the same values is corroboration.
// Different values with unrelated subjects is just two facts. Only the
// combination is a conflict.
func disagree(a, b string) bool {
	return textmatch.Overlap(textmatch.Subject(a), textmatch.Subject(b)) >= textmatch.Related &&
		textmatch.DifferingValues(a, b)
}

// supersede keeps the newest statement of each claim and returns the rest.
//
// Recall hands back everything relevant, which for a value that has changed
// twice means the live figure and both dead ones with nothing to tell them
// apart. Memories are timestamped, so where two disagree the ordering is not
// ambiguous: the later one is the current answer and the earlier one is
// history. The dropped memories are reported rather than silently discarded —
// an agent that can see "two earlier values were superseded" can ask for them.
func supersede(task string, mems []memory.Memory) (kept, dropped []memory.Memory) {
	ordered := make([]memory.Memory, len(mems))
	copy(ordered, mems)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Created > ordered[j].Created })

	asked := textmatch.Subject(task)
	// Statements restating the same value do not always restate the same words.
	// "We are targeting a $199 retail price" and "Retail is moving to $229 after
	// the optics quote came back" share exactly one content word, which is not
	// enough to link them on their own — and yet both were retrieved for "what
	// is the retail price?", which is the thing they have in common. The
	// question is the missing subject: when two statements are both squarely on
	// topic for it and assert different values, the later one is the answer.
	onTopic := func(m memory.Memory) bool {
		return len(asked) > 0 && textmatch.Overlap(asked, textmatch.Subject(m.Text)) >= textmatch.Related
	}

	// The question only bridges them when it happens to say "retail price".
	// Asked "what are we charging customers?", nothing linked the $229 line to
	// the others and it was handed over as a live figure beside $249 (#223). The
	// statements that do link strongly already name the subject between them:
	// the words $199 and $249 share are "retail price", and that is the subject
	// the $229 line is about. A chain subject is at least two words, because a
	// single shared word is a coincidence, not a topic.
	var chains []map[string]bool
	for i, a := range ordered {
		for _, b := range ordered[i+1:] {
			if !disagree(a.Text, b.Text) {
				continue
			}
			if c := akinShared(textmatch.Subject(a.Text), textmatch.Subject(b.Text)); len(c) >= 2 {
				chains = append(chains, c)
			}
		}
	}
	// Half a two-word chain is one word, so a chain alone would also link "Retail
	// staff are paid $31 an hour" to the retail price and delete a true fact. A
	// value restated as it changed stays in the same range — $199, $229, $249 —
	// and an unrelated figure that shares a word rarely does, so the chain link
	// also asks for comparable magnitudes.
	onChain := func(k, m memory.Memory) bool {
		sk, sm := textmatch.Subject(k.Text), textmatch.Subject(m.Text)
		for _, c := range chains {
			if textmatch.Overlap(c, sk) >= textmatch.Related && textmatch.Overlap(c, sm) >= textmatch.Related &&
				textmatch.DifferingValues(k.Text, m.Text) && comparable(k.Text, m.Text) {
				return true
			}
		}
		return false
	}

	for _, m := range ordered {
		superseded := false
		for _, k := range kept {
			if k.Created < m.Created {
				continue
			}
			if disagree(k.Text, m.Text) ||
				(onTopic(k) && onTopic(m) && textmatch.DifferingValues(k.Text, m.Text)) ||
				onChain(k, m) {
				superseded = true
				break
			}
		}
		if superseded {
			dropped = append(dropped, m)
		} else {
			kept = append(kept, m)
		}
	}

	// Restore the ranking retrieval chose; recency decided what survives, not
	// what leads.
	order := map[int64]int{}
	for i, m := range mems {
		order[m.ID] = i
	}
	sort.SliceStable(kept, func(i, j int) bool { return order[kept[i].ID] < order[kept[j].ID] })
	return kept, dropped
}

// akinShared returns the words of a that b also has, in some inflection.
func akinShared(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for w := range a {
		for v := range b {
			if textmatch.Akin(w, v) {
				out[w] = true
				break
			}
		}
	}
	return out
}

// comparable reports whether some value in a and some value in b have the same
// unit and are within a factor of two of each other.
func comparable(a, b string) bool {
	for va := range textmatch.Values(a) {
		na, ua, ok := magnitude(va)
		if !ok {
			continue
		}
		for vb := range textmatch.Values(b) {
			nb, ub, ok := magnitude(vb)
			if ok && ua == ub && max(na, nb) <= 2*min(na, nb) {
				return true
			}
		}
	}
	return false
}

// magnitude splits a normalised value from textmatch.Values into its number
// and its unit suffix.
func magnitude(v string) (float64, string, bool) {
	i := strings.IndexFunc(v, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i < 0 {
		i = len(v)
	}
	n, err := strconv.ParseFloat(v[:i], 64)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, strings.TrimSpace(v[i:]), true
}

// overtaken reports whether a later statement calls off a planned next step,
// and returns the statement that did it.
//
// This is the failure with the worst consequence in the whole suite. A
// checkpoint's "next step" is the one line a resuming agent is most likely to
// act on immediately — and if the user killed that plan afterwards, handing it
// over unqualified sends the agent straight back into work that has been
// explicitly abandoned. It is not a stale fact, it is an instruction to do the
// wrong thing.
//
// Only statements *newer* than the checkpoint count: a plan naturally
// supersedes the discussion that preceded it.
func overtaken(next string, since int64, mems []memory.Memory) (memory.Memory, bool) {
	plan := textmatch.Subject(next)
	if len(plan) == 0 {
		return memory.Memory{}, false
	}
	for _, m := range mems {
		if m.Created <= since {
			continue
		}
		if textmatch.Negated(m.Text) &&
			textmatch.Overlap(plan, textmatch.Subject(m.Text)) >= textmatch.Related {
			return m, true
		}
	}
	return memory.Memory{}, false
}

// answered reports whether anything retrieved is actually about what was asked.
//
// Retrieval always returns its nearest neighbour, and over a small store the
// nearest neighbour to a question nobody ever answered is still something. That
// is how "which plant does the optical bonding?" comes back with the plant's
// shift pattern: a real memory, a decent score, and not an answer. Comparing
// the question's subject against what came back is crude, but it separates
// "here is what you asked for" from "here is the closest thing I had", and no
// system benchmarked drew that line at all.
func answered(task string, mems []memory.Memory) bool {
	asked := textmatch.Subject(task)
	if len(asked) == 0 {
		return true // nothing to check against; do not cry wolf
	}
	for _, m := range mems {
		if textmatch.Overlap(asked, textmatch.Subject(m.Text)) >= textmatch.Related {
			return true
		}
	}
	return false
}

// contradictions finds retrieved notes that disagree with each other.
//
// Unlike memories, notes have no reliable ordering — a summary page and a weekly
// report are both current, and whichever was edited last is not therefore right.
// So nothing is dropped. The conflict is named, both figures stay, and the
// agent is told to check rather than being handed a false resolution.
func contradictions(hits []index.Hit) []string {
	type claim struct {
		slug string
		line string
	}
	var claims []claim
	for _, h := range hits {
		for _, line := range strings.Split(h.Body, "\n") {
			line = strings.TrimSpace(line)
			if len(textmatch.Values(line)) > 0 && len(line) > 20 {
				claims = append(claims, claim{h.Slug, flatten(line)})
			}
		}
	}

	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(claims); i++ {
		for j := i + 1; j < len(claims); j++ {
			a, b := claims[i], claims[j]
			if a.slug == b.slug || !disagree(a.line, b.line) {
				continue
			}
			key := a.slug + "|" + b.slug
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, fmt.Sprintf("%s says %q; %s says %q", a.slug, oneLine(a.line), b.slug, oneLine(b.line)))
		}
	}
	return out
}
