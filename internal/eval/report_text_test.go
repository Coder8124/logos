package eval

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// clip byte-sliced at a fixed offset (s[:n-1]), which cuts a multibyte rune in
// half when a scenario name or score note contains emoji or CJK text.
func TestClipNeverSplitsAMultibyteRune(t *testing.T) {
	in := strings.Repeat("🌍", 50)
	got := clip(in, 30)
	if !utf8.ValidString(got) {
		t.Fatalf("clip produced invalid UTF-8: %q", got)
	}
}
