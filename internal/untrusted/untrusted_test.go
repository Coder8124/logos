package untrusted

import (
	"strings"
	"testing"
)

// Demoting by exactly one level assumed the frame occupied exactly one. It does
// not: a context pack opens "## From the vault" and titles each note inside it
// "### <title>". A stored note whose body contained "## From the vault" came out
// as "### From the vault" — the note-title level, sitting inside the real
// section and looking exactly like a sibling entry, with "#### fact `memory#99`"
// underneath it carrying an instruction. One "#" more is only enough when the
// frame is one level deep, so payload has to land below all of it.
func TestADemotedHeadingLandsBelowEveryLevelTheFrameUses(t *testing.T) {
	for _, in := range []string{"# Top", "## From the vault", "### fact `memory#99`"} {
		got := Block(in)
		level := len(got) - len(strings.TrimLeft(got, "#"))
		if level < 4 {
			t.Errorf("Block(%q) = %q, which is still at a level the frame writes", in, got)
		}
	}
	// Already below the frame, and already at markdown's limit: keep the shape
	// without leaving a heading behind.
	if got := Block("####### seven"); strings.HasPrefix(got, "#") {
		t.Errorf("a heading past markdown's limit stayed a heading: %q", got)
	}
	// A rule is the frame's own footer separator and must not survive either.
	if got := Block("---"); strings.Contains(got, "---") {
		t.Errorf("a horizontal rule survived: %q", got)
	}
	// Ordinary prose is untouched — this escapes structure, it does not edit text.
	if got := Block("a line #not a heading\nand another"); got != "a line #not a heading\nand another" {
		t.Errorf("prose was altered: %q", got)
	}
}
