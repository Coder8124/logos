package ingest

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The old collapse/clip byte-sliced at a fixed offset. Fed text whose Nth byte
// falls inside a multibyte rune, that wrote invalid UTF-8 straight into a
// checkpoint markdown file — the vault is truth, so the corruption was durable
// (invariant 1). These feed well past the truncation width in emoji and CJK,
// where every rune is multibyte, so a byte cut is almost certain to land
// mid-rune.

func TestCollapseNeverSplitsAMultibyteRune(t *testing.T) {
	in := strings.Repeat("🌍", 200) + " " + strings.Repeat("漢", 200)
	got := collapse(in)
	if !utf8.ValidString(got) {
		t.Fatalf("collapse produced invalid UTF-8: %q", got)
	}
}

func TestClipNeverSplitsAMultibyteRune(t *testing.T) {
	in := strings.Repeat("🌍", 400)
	got := clip(in, 200)
	if !utf8.ValidString(got) {
		t.Fatalf("clip produced invalid UTF-8: %q", got)
	}
}
