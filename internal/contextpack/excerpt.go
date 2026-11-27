package contextpack

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/textmatch"
)

// Excerpting long notes.
//
// wantNotes used to paste every note whole. A transcript-sized note is several
// thousand tokens, so on a question whose answer is spread over three of them
// the first two or three notes spent the section and the rest were dropped
// whole — measured on LongMemEval multi-session, retrieval ranked the evidence
// into the top results and the pack ran out of room before reaching it. When
// the notes together would overflow their allowance, the long ones are cut
// down to the passages that match the task, so more distinct notes fit.
//
// Nothing is cut while everything fits: a pack that has room for the whole
// note gets the whole note.

// minExcerptTokens is the least a note is cut down to. Below it a passage or
// two is a fragment, and the note is better dropped by the ordinary cutoff.
const minExcerptTokens = 400

// excerptCaps returns the per-note ceiling, in tokens, that makes the notes fit
// the allowance, or 0 when they already fit. It is water-filling: a note
// shorter than the ceiling keeps all of it and the room it did not need goes to
// the longer notes, so short notes are never touched.
func excerptCaps(costs []int, allowance int) int {
	total := 0
	for _, c := range costs {
		total += c
	}
	if total <= allowance {
		return 0
	}
	sorted := append([]int(nil), costs...)
	sort.Ints(sorted)
	remaining, left := allowance, len(sorted)
	for _, c := range sorted {
		if c*left <= remaining {
			remaining -= c
			left--
			continue
		}
		break
	}
	if left == 0 {
		return 0
	}
	return max(remaining/left, minExcerptTokens)
}

type passage struct {
	text   string
	index  int
	tokens int
	score  float64
}

// excerptBody keeps the passages of body that best match the task terms, up to
// budget tokens, in document order. It reports whether it cut anything.
//
// Passages are blank-line separated blocks — one turn of a transcript, one
// paragraph of a note. They are scored by how many distinct task words they
// contain, discounted by length so a long reply does not win by sheer size.
func excerptBody(body string, terms map[string]bool, budget int) (string, bool) {
	var ps []passage
	for i, raw := range strings.Split(body, "\n\n") {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		ps = append(ps, passage{text: t, index: i, tokens: estimate(t) + 1})
	}
	for i := range ps {
		ps[i].score = passageScore(ps[i], terms)
	}
	order := append([]passage(nil), ps...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].score > order[j].score })

	chosen := map[int]bool{}
	used := 0
	for _, p := range order {
		if p.score == 0 && len(chosen) > 0 {
			break
		}
		if used+p.tokens > budget {
			if len(chosen) == 0 {
				// Nothing matched small enough: a hard cut of the best
				// passage beats an empty excerpt.
				cut, _ := fit(p.text, budget)
				return cut, true
			}
			continue
		}
		chosen[p.index] = true
		used += p.tokens
	}
	if len(chosen) == len(ps) {
		return body, false
	}
	var parts []string
	prev := -1
	for _, p := range ps {
		if !chosen[p.index] {
			continue
		}
		if prev >= 0 && p.index != prev+1 {
			parts = append(parts, "[…]")
		}
		parts = append(parts, p.text)
		prev = p.index
	}
	return strings.Join(parts, "\n\n"), true
}

func passageScore(p passage, terms map[string]bool) float64 {
	if len(terms) == 0 {
		return 0
	}
	have := map[string]bool{}
	for w := range textmatch.Subject(p.text) {
		have[textmatch.AkinStem(w)] = true
	}
	hits := 0
	for w := range terms {
		if have[textmatch.AkinStem(w)] {
			hits++
		}
	}
	if hits == 0 {
		return 0
	}
	// Density, not count: sqrt keeps a rich long passage ahead of a one-word
	// short one without letting length alone decide.
	return float64(hits) / math.Sqrt(float64(p.tokens))
}

// excerptLine says a note is a cut, how much of it, and where the rest is. A
// pack that quietly shortened a note would read as the whole note, and the
// reader would conclude the missing passages did not exist.
func excerptLine(shown, whole int, slug string) string {
	return fmt.Sprintf("_excerpt: the passages matching the task, about %d of %d tokens — the full note is at `%s`_", shown, whole, inline(slug))
}
