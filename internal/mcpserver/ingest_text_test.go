package mcpserver

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// clipClaim byte-sliced at a fixed offset (s[:79]), which cuts a multibyte
// rune in half when the claim text is emoji or CJK and writes invalid UTF-8
// into the text served back to the calling agent (invariant 1's concern
// applies just as much to what leaves the process as to what lands in the
// vault).
func TestClipClaimNeverSplitsAMultibyteRune(t *testing.T) {
	in := strings.Repeat("漢", 200)
	got := clipClaim(in)
	if !utf8.ValidString(got) {
		t.Fatalf("clipClaim produced invalid UTF-8: %q", got)
	}
}
