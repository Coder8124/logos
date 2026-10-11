package secret

import "regexp"

// PrivateMarker replaces a <private>…</private> span.
const PrivateMarker = "[private]"

// privateReason is the Redaction reason Summary reports apart from secrets: a
// span the person marked is not a token that happened to look like a key.
const privateReason = "<private> span"

// privateSpan is a <private> span, any case, attributes allowed. One that is
// never closed runs to the end: a hook keeps a prompt cut to 160 characters,
// and the text after an opened span is the person's private text until it is
// shown not to be.
var privateSpan = regexp.MustCompile(`(?is)<private\b[^>]*>.*?(?:</private\s*>|$)`)

// StripPrivate replaces every <private> span in s with PrivateMarker and
// reports one Redaction per span.
//
// The person marks what they never want kept; that is a promise about every
// path text takes into Logos — a hook's prompt, a transcript read for harvest
// or distil, an import, a note — so this is applied where text enters, not
// where one writer happens to remember it.
func StripPrivate(field, s string) (string, []Redaction) {
	var found []Redaction
	out := privateSpan.ReplaceAllStringFunc(s, func(string) string {
		found = append(found, Redaction{Field: field, Reason: privateReason})
		return PrivateMarker
	})
	return out, found
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
