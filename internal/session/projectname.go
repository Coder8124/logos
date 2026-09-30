package session

import (
	"strings"
	"unicode"
)

// NameInside is the one known project whose name appears, as whole words, in
// a name that matched none of them — "saathi" inside "Saathi backend" — or ""
// when no known name does, or more than one does.
//
// Agents describe a project rather than quote it, and a resume that answered
// "nothing recorded … say so rather than inferring" to "Saathi backend" told
// the agent to give up on work filed one word away. It is a suggestion, never
// a substitution: a name the caller gave is honoured, and handing them another
// project's work unasked is the worse failure. Whole words, so "saath" is not
// taken for "saathi", and exactly one, so a name that holds two projects gets
// the plain list rather than a coin flip.
func NameInside(given string, known []string) string {
	have := nameWords(given)
	found := ""
	for _, k := range known {
		want := nameWords(k)
		if len(want) == 0 || len(want) >= len(have) || !containsRun(have, want) {
			continue
		}
		if found != "" {
			return ""
		}
		found = k
	}
	return found
}

func nameWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func containsRun(have, want []string) bool {
	for i := 0; i+len(want) <= len(have); i++ {
		match := true
		for j := range want {
			if have[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
