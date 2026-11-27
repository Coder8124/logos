package secret

import (
	"regexp/syntax"
	"strings"
	"testing"
)

// literalPrefixes lists the strings every match of re can begin with, as far
// as the pattern spells them out; complete reports that the list is the whole
// match, so the caller may keep concatenating. Case is folded to lower, as the
// scanner folds it.
func literalPrefixes(re *syntax.Regexp) (set []string, complete bool) {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{strings.ToLower(string(re.Rune))}, true
	case syntax.OpCharClass:
		seen := map[string]bool{}
		for i := 0; i+1 < len(re.Rune); i += 2 {
			if int(re.Rune[i+1]-re.Rune[i]) > 40 || len(seen) > 40 {
				return []string{""}, false
			}
			for r := re.Rune[i]; r <= re.Rune[i+1]; r++ {
				seen[strings.ToLower(string(r))] = true
			}
		}
		if len(seen) > 40 {
			return []string{""}, false
		}
		for s := range seen {
			set = append(set, s)
		}
		return set, true
	case syntax.OpCapture:
		return literalPrefixes(re.Sub[0])
	case syntax.OpEmptyMatch, syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine:
		return []string{""}, true
	case syntax.OpConcat:
		acc := []string{""}
		for _, sub := range re.Sub {
			s, c := literalPrefixes(sub)
			var next []string
			for _, a := range acc {
				for _, b := range s {
					next = append(next, a+b)
				}
			}
			acc = next
			if !c || len(acc) > 400 {
				return acc, false
			}
		}
		return acc, true
	case syntax.OpAlternate:
		complete = true
		for _, sub := range re.Sub {
			s, c := literalPrefixes(sub)
			set = append(set, s...)
			complete = complete && c
		}
		return set, complete
	case syntax.OpPlus, syntax.OpRepeat:
		if re.Op == syntax.OpPlus || re.Min >= 1 {
			s, _ := literalPrefixes(re.Sub[0])
			return s, false
		}
	}
	return []string{""}, false
}

// leadsWithLiteral reports that every match of re starts with spelled-out
// text, not a wide class: only then is the pattern's prefix a thing a keyword
// is expected to cover. A rule that opens on [a-f0-9]{32} has its keyword
// further in, and is held to its own sample instead.
func leadsWithLiteral(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		return true
	case syntax.OpCharClass:
		n := 0
		for i := 0; i+1 < len(re.Rune); i += 2 {
			n += int(re.Rune[i+1]-re.Rune[i]) + 1
		}
		return n <= 4
	case syntax.OpCapture, syntax.OpPlus:
		return leadsWithLiteral(re.Sub[0])
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if !leadsWithLiteral(sub) {
				return false
			}
		}
		return true
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			switch sub.Op {
			case syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpBeginText, syntax.OpBeginLine, syntax.OpEmptyMatch:
				continue
			}
			return leadsWithLiteral(sub)
		}
	}
	return false
}

// TestEveryRuleKeywordCoversEveryLiteralPrefixItsPatternCanMatch: the keyword
// list is what decides whether a rule runs at all, so a pattern that can match
// a prefix its keywords do not contain is a rule that silently never fires on
// that shape (fly's fm2r_ was one). Rules that open on a class rather than a
// literal have no prefix to check and are covered by their own sample.
func TestEveryRuleKeywordCoversEveryLiteralPrefixItsPatternCanMatch(t *testing.T) {
	// Rules whose keyword is not at the front of its pattern: the legacy
	// OpenAI key is sk- then twenty characters then T3BlbkFJ, and a webhook
	// URL is named by its host, which follows the scheme.
	midPattern := map[string]bool{"openai-api-key": true, "discord-webhook": true, "microsoft-teams-webhook": true}
	for _, r := range tokenRules {
		re, err := syntax.Parse(r.pattern, syntax.Perl)
		if err != nil {
			t.Fatalf("%s: %v", r.id, err)
		}
		if !leadsWithLiteral(re) {
			continue
		}
		prefixes, _ := literalPrefixes(re)
		for _, p := range prefixes {
			covered := false
			for _, k := range r.keywords {
				covered = covered || strings.Contains(p, k)
			}
			if !covered && !midPattern[r.id] {
				t.Errorf("%s: its pattern can match text starting %q, which none of its keywords %q covers, so the rule would never run on it", r.id, p, r.keywords)
			}
		}
	}
}

// TestCommonWordsDoNotWakeAnyTokenRule: a two-letter keyword makes every line
// with "task" or "they" pay for a regexp run; the keywords are long enough
// that ordinary words do not match them.
func TestCommonWordsDoNotWakeAnyTokenRule(t *testing.T) {
	for _, line := range []string{
		"the task key they wondered about with eyes on the desk and the risk",
		"Colorado tornado shadows, access to the api, key-value pairs and us-east-1",
		"sk status ey ado",
	} {
		h := scanner().scan(line)
		for i, r := range tokenRules {
			if h.has(i) {
				t.Errorf("%q wakes rule %s", line, r.id)
			}
		}
	}
}
