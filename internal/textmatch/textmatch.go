// Package textmatch is the small lexical toolkit two subsystems needed at once.
//
// It exists because comparing "is this sentence about the same thing as that
// one" turned up in two unrelated places — deciding whether a recalled value
// supersedes another, and deciding whether a proposed approach is one somebody
// already ruled out — and neither should have to import the other to ask.
//
// Everything here is deliberately shallow. There is no stemmer, no tokenizer,
// no model. The judgements it supports are all of the form "close enough to be
// worth a human's attention", where a near-miss costs a wasted glance and a
// false confidence costs an afternoon.
package textmatch

import (
	"regexp"
	"strings"
)

// Stopwords are the words that say nothing about the subject.
//
// Two groups. The ordinary function words, and — less obviously — the
// interrogatives and colourless verbs that make up most of a question. "What
// should I do about the waveguide" is a question about the waveguide; leaving
// the scaffolding in drags every comparison toward a middling score where
// nothing is clearly related and nothing is clearly not.
var Stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "was": true, "are": true, "our": true,
	"that": true, "this": true, "with": true, "from": true, "have": true, "has": true,
	"will": true, "would": true, "into": true, "over": true, "under": true, "after": true,
	"before": true, "than": true, "then": true, "they": true, "were": true, "been": true,
	"but": true, "not": true, "its": true, "his": true, "her": true, "their": true,
	"about": true, "just": true, "only": true, "also": true, "more": true, "most": true,
	"what": true, "when": true, "where": true, "which": true, "whose": true,
	"does": true, "did": true, "should": true, "could": true, "shall": true,
	"bring": true, "make": true, "take": true, "give": true, "need": true,
	"want": true, "know": true, "tell": true, "keep": true, "come": true,
	"going": true, "doing": true, "being": true, "said": true, "says": true,
	"try": true, "trying": true, "tried": true, "instead": true, "maybe": true,
}

// Subject reduces a statement to its distinctive content words.
func Subject(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(w) > 3 && !Stopwords[w] && !numeric(w) {
			out[w] = true
		}
	}
	return out
}

func numeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// Akin reports whether two words are the same term in different clothes.
//
// A shared prefix of five characters catches the inflections that matter —
// manufactures/manufacturer, proposal/proposals, quote/quoted — without pulling
// in a stemmer. Exact matching missed all of them, and a question asked in the
// user's words rarely uses the same inflection as the note that answers it.
func Akin(a, b string) bool {
	if a == b {
		return true
	}
	n := min(len(a), len(b))
	return n >= 5 && a[:n] == b[:n]
}

// Overlap measures containment, not Jaccard.
//
// Jaccard asks how much two statements share as a proportion of everything
// either one says, which punishes the longer statement for being longer:
// "we are targeting a $199 retail price" against "final call: retail price is
// $249, that is locked for launch" scores 0.29 and slips under any sensible
// bar, so a superseded price gets handed over as though it were current. The
// question that actually matters is whether the shorter statement is *about*
// the same thing as the longer one, and that is containment.
func Overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var shared int
	for w := range a {
		for v := range b {
			if Akin(w, v) {
				shared++
				break
			}
		}
	}
	return float64(shared) / float64(min(len(a), len(b)))
}

// Related is the bar for "these two statements are about the same thing".
//
// Half the shorter statement's distinctive words. Tuned by what it is used for:
// every consumer of this package acts on a match by suppressing something or
// interrupting someone, so a false positive is louder than a miss.
const Related = 0.5

// valuePattern matches the kinds of value that get restated: money, plain and
// decimal numbers, percentages, and quantities with a unit suffix.
var valuePattern = regexp.MustCompile(`\$?\d[\d,]*\.?\d*\s*(?:%|percent|k|m|bn|days?|weeks?|months?|years?|hours?)?`)

// Values returns the normalised values a statement asserts.
func Values(s string) map[string]bool {
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

// DifferingValues reports that both statements assert a value and share none.
func DifferingValues(a, b string) bool {
	va, vb := Values(a), Values(b)
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

// Negations are the ways people call something off. Cheap and blunt, but the
// alternative is a model call on every read, and these are the phrasings that
// actually show up when a plan is cancelled or an approach is abandoned.
var Negations = []string{
	"not ", "no longer", "instead of", "rather than", "decided against",
	"drop ", "dropped", "cancel", "cancelled", "abandon", "scrap", "stop ",
	"reverted", "backed out", "we are staying", "sticking with",
}

// Negated reports whether a statement calls something off.
func Negated(s string) bool {
	low := strings.ToLower(s)
	for _, n := range Negations {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// Flatten collapses whitespace without shortening.
func Flatten(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
