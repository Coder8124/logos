package secret

import (
	"regexp"
	"strings"
)

// PrivateMarker replaces a <private>…</private> span.
const PrivateMarker = "[private]"

// privateReason is the Redaction reason Summary reports apart from secrets: a
// span the person marked is not a token that happened to look like a key.
const privateReason = "<private> span"

// privateOpen and privateTag find the tags a span is made of, any case,
// attributes allowed. "private" must end at whitespace or ">": `\b` also
// matched before "-" and "/", so <private-notes> and <private/> opened spans
// nothing closed, and the rest of the turn was dropped as one the person
// marked.
var (
	privateOpen = regexp.MustCompile(`(?i)<private(?:\s[^>]*)?>`)
	privateTag  = regexp.MustCompile(`(?i)<private(?:\s[^>]*)?>|</private\s*>`)
)

// StripPrivate replaces every <private> span in s with PrivateMarker and
// reports one Redaction per span.
//
// The person marks what they never want kept; that is a promise about every
// path text takes into Logos — a hook's prompt, a transcript read for harvest
// or distil, an import, a note — so this is applied where text enters, not
// where one writer happens to remember it.
//
// Spans nest: a lazy match closed the outer span at the inner one's end and
// kept what the person wrote after it. One that is never closed runs to the
// end: a hook keeps a prompt cut to 160 characters, and the text after an
// opened span is the person's private text until it is shown not to be.
func StripPrivate(field, s string) (string, []Redaction) {
	if !strings.Contains(strings.ToLower(s), "<private") {
		return s, nil
	}
	var (
		b     strings.Builder
		found []Redaction
	)
	for {
		start := opening(s)
		if start == nil {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:start[0]])
		b.WriteString(PrivateMarker)
		found = append(found, Redaction{Field: field, Reason: privateReason})
		rest := s[start[1]:]
		end, depth := len(rest), 1
		for _, m := range privateTag.FindAllStringIndex(rest, -1) {
			tag := rest[m[0]:m[1]]
			switch {
			case strings.HasPrefix(tag, "</"):
				depth--
			case !strings.HasSuffix(tag, "/>"):
				depth++
			}
			if depth == 0 {
				end = m[1]
				break
			}
		}
		s = rest[end:]
	}
	return b.String(), found
}

// opening is the first tag in s that opens a span. <private /> holds nothing
// and opens nothing.
func opening(s string) []int {
	for _, m := range privateOpen.FindAllStringIndex(s, -1) {
		if !strings.HasSuffix(s[m[0]:m[1]], "/>") {
			return m
		}
	}
	return nil
}

// NothingLeft says whether text once masked says nothing at all: only markers
// and punctuation. A memory or a note that was a pasted key or a span the
// person marked private is refused, not kept as "[REDACTED]" or "[private]" —
// a record that says nothing, ranked and recalled like one that does.
func NothingLeft(s string) bool {
	s = strings.ReplaceAll(strings.ReplaceAll(s, Marker, ""), PrivateMarker, "")
	return strings.Trim(s, " \t\n.,;:'\"`") == ""
}

// KeptPrivate is n spans' Redactions, for a caller that stripped them where the
// text was read and reports them where it is written: a transcript loses its
// spans as it is parsed, long before the record built from it has a receipt.
func KeptPrivate(field string, n int) []Redaction {
	found := make([]Redaction, n)
	for i := range found {
		found[i] = Redaction{Field: field, Reason: privateReason}
	}
	return found
}
