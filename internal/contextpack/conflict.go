package contextpack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
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

// valuePattern matches the kinds of value that get restated: money, plain and
// decimal numbers, percentages, and quantities with a unit suffix.
var valuePattern = regexp.MustCompile(`\$?\d[\d,]*\.?\d*\s*(?:%|percent|k|m|bn|days?|weeks?|months?|years?|hours?)?`)

// stopwords are the words that say nothing about the subject. Deliberately
// short: the overlap test wants distinctive terms, and an aggressive list would
// start throwing away the nouns that carry the topic.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "was": true, "are": true, "our": true,
	"that": true, "this": true, "with": true, "from": true, "have": true, "has": true,
	"will": true, "would": true, "into": true, "over": true, "under": true, "after": true,
	"before": true, "than": true, "then": true, "they": true, "were": true, "been": true,
	"but": true, "not": true, "its": true, "his": true, "her": true, "their": true,
	"about": true, "just": true, "only": true, "also": true, "more": true, "most": true,
	// Interrogatives and colourless verbs. A question is mostly scaffolding —
	// "what should I do about X" is a question about X — and leaving the
	// scaffolding in drags every comparison toward a middling score where
	// nothing is clearly related and nothing is clearly not.
	"what": true, "when": true, "where": true, "which": true, "whose": true,
	"does": true, "did": true, "should": true, "could": true, "shall": true,
	"bring": true, "make": true, "take": true, "give": true, "need": true,
	"want": true, "know": true, "tell": true, "keep": true, "come": true,
	"going": true, "doing": true, "being": true, "said": true, "says": true,
}

// subject reduces a statement to its distinctive content words.
func subject(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(w) > 3 && !stopwords[w] && !isNumeric(w) {
			out[w] = true
		}
	}
	return out
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// akin reports whether two words are the same term in different clothes.
//
// A shared prefix of five characters catches the inflections that matter here —
// manufactures/manufacturer, proposal/proposals, quote/quoted — without pulling
// in a stemmer. Exact matching missed all of them, and a question asked in the
// user's words rarely uses the same inflection as the note that answers it.
func akin(a, b string) bool {
	if a == b {
		return true
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	return n >= 5 && a[:n] == b[:n]
}

// overlap measures containment, not Jaccard.
//
// Jaccard asks how much two statements have in common as a share of everything
// either one says, which punishes the longer statement for being longer:
// "we are targeting a $199 retail price" against "final call: retail price is
// $249, that is locked for launch" scored 0.29 and slipped under the bar, so
// the superseded price was handed over as though it were current. The question
// that actually matters is whether the shorter statement is *about* the same
// thing as the longer one, which is containment.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var shared int
	for w := range a {
		for v := range b {
			if akin(w, v) {
				shared++
				break
			}
		}
	}
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	return float64(shared) / float64(smaller)
}

// values returns the normalised values a statement asserts.
func values(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range valuePattern.FindAllString(s, -1) {
		m = strings.TrimSpace(strings.ToLower(m))
		m = strings.ReplaceAll(m, ",", "")
		m = strings.TrimPrefix(m, "$")
		m = strings.TrimSuffix(strings.TrimSpace(m), ".")
		if m != "" && m != "0" {
			out[m] = true
		}
	}
	return out
}

// disagree reports whether two statements are about the same thing and assert
// different values for it.
//
// Both halves are required. Same subject with the same values is corroboration.
// Different values with unrelated subjects is just two facts. Only the
// combination is a conflict.
const sameSubject = 0.5

func disagree(a, b string) bool {
	return overlap(subject(a), subject(b)) >= sameSubject && differingValues(a, b)
}

// differingValues reports that both statements assert a value and share none.
func differingValues(a, b string) bool {
	va, vb := values(a), values(b)
	if len(va) == 0 || len(vb) == 0 {
		return false
	}
	for v := range va {
		if vb[v] {
			return false // they share a value: a restatement, not a contradiction
		}
	}
	return true
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

	asked := subject(task)
	// Statements restating the same value do not always restate the same words.
	// "We are targeting a $199 retail price" and "Retail is moving to $229 after
	// the optics quote came back" share exactly one content word, which is not
	// enough to link them on their own — and yet both were retrieved for "what
	// is the retail price?", which is the thing they have in common. The
	// question is the missing subject: when two statements are both squarely on
	// topic for it and assert different values, the later one is the answer.
	onTopic := func(m memory.Memory) bool {
		return len(asked) > 0 && overlap(asked, subject(m.Text)) >= sameSubject
	}

	for _, m := range ordered {
		superseded := false
		for _, k := range kept {
			if k.Created < m.Created {
				continue
			}
			if disagree(k.Text, m.Text) ||
				(onTopic(k) && onTopic(m) && differingValues(k.Text, m.Text)) {
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
	asked := subject(task)
	if len(asked) == 0 {
		return true // nothing to check against; do not cry wolf
	}
	for _, m := range mems {
		if overlap(asked, subject(m.Text)) >= sameSubject {
			return true
		}
	}
	return false
}

// negations are the ways people call something off. Cheap and blunt, but the
// alternative is a model call on every resume, and this catches the phrasing
// that actually shows up when a plan is cancelled.
var negations = []string{
	"not ", "no longer", "instead of", "rather than", "decided against",
	"drop ", "dropped", "cancel", "cancelled", "abandon", "scrap", "stop ",
	"reverted", "backed out", "we are staying", "sticking with",
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
	plan := subject(next)
	if len(plan) == 0 {
		return memory.Memory{}, false
	}
	for _, m := range mems {
		if m.Created <= since {
			continue
		}
		low := strings.ToLower(m.Text)
		negated := false
		for _, n := range negations {
			if strings.Contains(low, n) {
				negated = true
				break
			}
		}
		if negated && overlap(plan, subject(m.Text)) >= sameSubject {
			return m, true
		}
	}
	return memory.Memory{}, false
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
			if len(values(line)) > 0 && len(line) > 20 {
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
