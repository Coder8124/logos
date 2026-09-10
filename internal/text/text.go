// Package text holds the small string-shortening helpers that were copied,
// subtly differently, into half a dozen packages. They share one rule that is
// load-bearing here: a truncation must land on a rune boundary. Several of
// these strings end up in a vault markdown file, and a byte slice through a
// multibyte rune writes invalid UTF-8 into truth (invariant 1).
package text

// Ellipsize shortens s to at most n runes, appending "…" when it had to cut so
// the reader can see the value is not complete (invariant 3). The "…" counts
// toward n, so the result is never wider than n runes.
func Ellipsize(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// Truncate shortens s to at most n runes with no marker. It is for values whose
// tail is noise rather than information — a UUID prefix, a commit hash, the
// fixed-width timestamp at the head of a checkpoint filename — where an "…"
// would be worse than the silent cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
