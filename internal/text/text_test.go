package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A byte slice at n would cut the emoji at the boundary in half and write
// invalid UTF-8 into whatever vault file the result lands in (invariant 1).
func TestEllipsizeNeverSplitsAMultibyteRune(t *testing.T) {
	in := strings.Repeat("🌍", 400)
	got := Ellipsize(in, 300)
	if !utf8.ValidString(got) {
		t.Fatalf("Ellipsize produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 300 {
		t.Fatalf("Ellipsize returned %d runes, wanted <= 300", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("Ellipsize dropped the truncation marker: %q", got)
	}
}

func TestEllipsizeLeavesAShortStringAlone(t *testing.T) {
	if got := Ellipsize("héllo", 10); got != "héllo" {
		t.Fatalf("Ellipsize mangled a string within the limit: %q", got)
	}
}

func TestTruncateNeverSplitsAMultibyteRune(t *testing.T) {
	got := Truncate(strings.Repeat("漢", 40), 15)
	if !utf8.ValidString(got) {
		t.Fatalf("Truncate produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 15 {
		t.Fatalf("Truncate returned %d runes, wanted 15", n)
	}
}
