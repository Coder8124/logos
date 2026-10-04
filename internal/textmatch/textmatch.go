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
	"slices"
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
	// The verbs of preference and possession. Two statements that differ only
	// in which of these they use — "likes short emails" against "prefers short
	// emails" — are one fact said twice, and leaving them in makes the choice of
	// verb look like the subject of the sentence. That matters most to
	// AssertsSomethingNew, where a word the other statement lacks is taken as
	// evidence of a second fact and so keeps a duplicate.
	"like": true, "likes": true, "liked": true, "prefer": true, "prefers": true,
	"preferred": true, "love": true, "loves": true, "hate": true, "hates": true,
	"dislike": true, "dislikes": true, "enjoy": true, "enjoys": true,
	"wants": true, "wanted": true, "uses": true, "used": true, "using": true,
	// Frequency adverbs are deliberately NOT here. "always deploy on Friday" and
	// "never deploy on Friday" reduce to the same subject without them, and a
	// guard that merges a rule with its opposite is worse than no guard.
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
// One word being a prefix of the other, at least five characters long, catches
// the inflections that add a suffix — proposal/proposals, quote/quoted,
// manufacture/manufacturer — without pulling in a stemmer. Exact matching
// missed all of them, and a question asked in the user's words rarely uses the
// same inflection as the note that answers it. Two words that both change the
// ending, manufactures/manufacturer, are not akin.
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
	return values(s, false)
}

// values is Values with a choice about zero. Zero is left out of Values for the
// conflict detector, which reads any line holding a value as a claim; the
// dedup guard needs it in, because "retries at 0" is a different decision from
// "retries at 3" and nothing else in the sentence says so.
func values(s string, zero bool) map[string]bool {
	out := map[string]bool{}
	for _, m := range valuePattern.FindAllString(s, -1) {
		m = strings.TrimSpace(strings.ToLower(m))
		m = strings.ReplaceAll(m, ",", "")
		m = strings.TrimPrefix(m, "$")
		m = strings.TrimSuffix(strings.TrimSpace(m), ".")
		if m != "" && (zero || m != "0") {
			out[m] = true
		}
	}
	return out
}

// DifferingValues reports that both statements assert a value and share none.
func DifferingValues(a, b string) bool {
	return differingValues(Values(a), Values(b))
}

// DifferingFactValues is DifferingValues for deciding whether two memories are
// one fact, where 0 counts as a value. See values.
func DifferingFactValues(a, b string) bool {
	return differingValues(values(a, true), values(b, true))
}

func differingValues(va, vb map[string]bool) bool {
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

// DifferentSubjects reports that each statement names something the other does
// not — the sign of two parallel facts rather than one fact restated.
//
// The sibling of DifferingValues, for the case where the difference is a noun
// rather than a number. "kestrel handles checkout through a dedicated service"
// and "kestrel handles pricing through a dedicated service" share every word
// but one, embed at well over any dedup threshold, and are two facts. Values()
// finds no numbers in either, so the numeric guard waves them through and one of
// them is destroyed.
//
// The test is mutual difference, not "the incoming says something new". That
// distinction is the whole of it, and getting it wrong the other way makes the
// guard useless:
//
//   - A restatement may add words. "I like my replies terse, without any
//     preamble" says everything "I prefer terse replies with no preamble" says
//     and one thing more. One subject set contains the other, and containment in
//     either direction is a restatement.
//   - A parallel fact swaps one thing for another. "pricing" sits exactly where
//     "checkout" sat, so each set holds a word the other lacks. Neither contains
//     the other, and nothing but keeping both is safe.
//
// Requiring mutual difference also means this never fires on a statement that
// merely elaborates, which is what a model does every time it re-extracts a fact
// it has already stated — the case the dedup threshold exists to catch.
//
// The words compared are factWords, not Subject: the short names that tell
// developer facts apart are exactly what Subject throws away.
func DifferentSubjects(a, b string) bool {
	sa, sb := factWords(a), factWords(b)
	if len(sa) == 0 || len(sb) == 0 {
		// Nothing distinctive on one side: no evidence either way, so this guard
		// abstains and leaves the decision to similarity and Values.
		return false
	}
	return hasNovel(sa, sb) && hasNovel(sb, sa)
}

// shortFunctionWords are the words of three letters or fewer that carry no
// subject. factWords keeps every other short word, because web, api, cli, ios,
// dev, qa, aws, npm, arm, x86, v2 and go are what distinguish one developer fact
// from its neighbour. The list is of grammar, not of topics: two phrasings of
// one fact differ in these ("with no preamble", "without any preamble"), and
// counting them would keep every restatement as a second fact.
var shortFunctionWords = map[string]bool{
	"a": true, "an": true, "the": true, "i": true, "me": true, "my": true, "we": true,
	"us": true, "our": true, "you": true, "he": true, "she": true, "it": true, "its": true,
	"his": true, "her": true, "him": true,
	"is": true, "am": true, "are": true, "was": true, "be": true, "do": true, "did": true, "has": true, "had": true, "can": true, "may": true, "get": true,
	"got": true, "let": true, "use": true, "put": true, "set": true,
	"of": true, "to": true, "in": true, "on": true, "at": true, "by": true, "for": true,
	"as": true, "up": true, "out": true, "off": true, "via": true, "per": true,
	"and": true, "or": true, "nor": true, "but": true, "so": true, "if": true, "yet": true,
	"no": true, "not": true, "any": true, "all": true, "few": true, "own": true, "too": true,
	"how": true, "why": true, "who": true, "now": true, "one": true, "etc": true,
	"e": true, "g": true, "ie": true, "eg": true, "s": true, "t": true,
}

// factWords is Subject plus the short words that are not grammar. Numbers stay
// out: Values compares those, and "16" in both sentences must not look like a
// shared subject that hides a changed one.
func factWords(s string) map[string]bool {
	out := Subject(s)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(w) <= 3 && !shortFunctionWords[w] && !numeric(w) {
			out[w] = true
		}
	}
	return out
}

// hasNovel reports whether any word in have is absent from want.
func hasNovel(have, want map[string]bool) bool {
	for w := range have {
		found := false
		for v := range want {
			if stemLike(w, v) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

// stemLike is Akin with a fixed prefix instead of a relative one: five shared
// leading characters, whatever the two lengths are. Akin compares a prefix of
// the *shorter* word's length, which for two words of equal length collapses to
// exact equality, so "handled" and "handles" read there as different words.
// That is harmless where Akin scores overlap and not harmless here, where a
// spurious difference keeps a duplicate. Akin is left alone because conflict
// detection and dead-end matching are tuned against it.
func stemLike(a, b string) bool {
	if a == b {
		return true
	}
	const stem = 5
	if len(a) < stem || len(b) < stem {
		return false
	}
	return a[:stem] == b[:stem]
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

// Reverses reports whether one statement takes back the other: the same claim
// denied, or the same sentence with its predicate swapped ("the staging
// database is postgres" against "…is mysql", "retries are enabled" against
// "…disabled"). Values only reads digits, so without this a dispute stated in
// words was never a dispute — and a denial, whose extra words are grammar, read
// as a restatement and was merged into the fact it denies.
//
// Narrower than Negated on purpose. Negated asks whether a plan was called off,
// where "instead of" and "sticking with" count; a fact that adds "instead of
// nomad" still asserts what the original did.
func Reverses(a, b string) bool {
	deniesA, wa := denial(a)
	deniesB, wb := denial(b)
	if deniesA != deniesB {
		// With the denial taken out, the rest must be the other sentence word
		// for word. Asking only whether the subjects matched let a "not" that
		// names a rejected alternative ("postgres, not mysql"), or a four-word
		// denial sharing its one noun with an unrelated memory, contest a fact
		// it agreed with or had nothing to do with. A denial with nothing left,
		// "Never." or "Don't!", denies nothing in particular.
		if deniesA {
			wa, wb = wb, wa
		}
		return len(wb) > 0 && (slices.Equal(wa, wb) || unemphatic(wa, wb))
	}
	// Two denials are not a swap: "the cache is not redis" and "…not
	// memcached" can both be true, and with their "not"s taken out they read
	// as one value exchanged for another. The price is "not on" against "not
	// off", which does contradict; telling opposites from alternatives needs
	// a list of antonyms this does not have.
	return !deniesA && swapsPredicate(wa, wb)
}

// unemphatic is whether an assertion is its denial's remaining words plus an
// emphatic do, does or did. denial takes the one before "not" out, so "we do
// use docker" must lose its own to meet "we do not use docker". Every do is
// tried, since an earlier one may be the verb ("we do the builds and do
// deploy"), and none is dropped unless the rest then matches word for word, so
// a "do" that is the verb ("they do the builds") stays in place.
func unemphatic(assert, denied []string) bool {
	if len(assert) != len(denied)+1 {
		return false
	}
	for i, w := range assert[:len(assert)-1] {
		if (w == "do" || w == "does" || w == "did") && slices.Equal(slices.Delete(slices.Clone(assert), i, i+1), denied) {
			return true
		}
	}
	return false
}

// denial is whether a statement denies its claim, and its words with the
// denial taken out. A bare "no" is left in: it usually denies a noun, not the
// claim — "terse replies with no preamble" is the same preference as "without
// any preamble", and counting it split every such restatement from its
// original. "no longer" is the "no" that reverses.
func denial(s string) (bool, []string) {
	low := strings.ToLower(strings.ReplaceAll(s, "’", "'"))
	low = strings.NewReplacer("won't", "will not", "can't", "can not", "cannot", "can not", "n't", " not").Replace(low)
	ws := words(low)
	var out []string
	denied := false
	for i := 0; i < len(ws); i++ {
		switch w := ws[i]; {
		case w == "not" || w == "never" || w == "anymore":
			denied = denied || w != "anymore"
			// "we don't deploy" asserts what "we deploy" does, once denied.
			if n := len(out); n > 0 && (out[n-1] == "do" || out[n-1] == "does" || out[n-1] == "did") {
				out = out[:n-1]
			}
		case w == "no" && i+1 < len(ws) && ws[i+1] == "longer":
			denied = true
			i++
		default:
			out = append(out, w)
		}
	}
	return denied, out
}

// copulas introduce a predicate. The swapped word must follow one, because a
// word swapped anywhere else is usually a different subject: "kestrel handles
// billing through a dedicated service" and "…search…" are two facts, and
// DifferentSubjects exists to keep them both.
var copulas = map[string]bool{"is": true, "are": true, "was": true, "were": true, "be": true, "been": true}

// ends are the words that can follow a predicate's value. The value is the
// head of the predicate, so it ends the sentence or a preposition follows it;
// a swapped word followed by more of the predicate is a modifier — "is a
// postgres instance" against "is the…", "is really slow" against "is very…" —
// and changing a modifier restates the claim.
var ends = map[string]bool{
	"in": true, "on": true, "at": true, "for": true, "with": true, "by": true, "to": true,
	"from": true, "of": true, "and": true, "or": true, "but": true, "when": true,
	"while": true, "during": true, "since": true, "until": true, "unless": true,
	"across": true, "via": true, "per": true, "as": true, "because": true, "except": true,
	"after": true, "before": true, "under": true, "over": true, "through": true, "than": true,
}

func swapsPredicate(wa, wb []string) bool {
	if len(wa) != len(wb) {
		return false
	}
	swapped := -1
	for i := range wa {
		if wa[i] == wb[i] {
			continue
		}
		if swapped >= 0 {
			return false
		}
		swapped = i
	}
	if swapped <= 0 || !copulas[wa[swapped-1]] {
		return false
	}
	if swapped+1 < len(wa) && !ends[wa[swapped+1]] {
		return false
	}
	// Numbers are Values' to compare; a word that is the same stem is a
	// rephrasing, not a different value.
	return !numeric(wa[swapped]) && !numeric(wb[swapped]) && !stemLike(wa[swapped], wb[swapped])
}

// words keeps dots inside a word, where they are part of a version or a host,
// and drops them at its end, where they close a sentence: "retries are on."
// is "retries are on", not a different value.
func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '.'
	}) {
		if w = strings.TrimRight(w, "."); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// Flatten collapses whitespace without shortening.
func Flatten(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
