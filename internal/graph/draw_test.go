package graph

import (
	"fmt"
	"strings"
	"testing"
)

func star(n int) Graph {
	g := Graph{Focus: "projects/kestrel"}
	g.Nodes = append(g.Nodes, Node{Slug: "projects/kestrel", Title: "Kestrel", Kind: "note", Degree: n})
	for i := range n {
		slug := fmt.Sprintf("notes/n%02d", i)
		g.Nodes = append(g.Nodes, Node{Slug: slug, Title: fmt.Sprintf("Note %02d", i), Kind: "note", Degree: 1, Hops: 1})
		g.Edges = append(g.Edges, Edge{Src: "projects/kestrel", Dst: slug, Pred: "links", Conf: 1, Provenance: Wikilink})
	}
	return g
}

// Two runs on the same graph must draw the same picture. A layout seeded at
// random reads as a different graph each time, and anyone comparing two runs
// to see what one new link changed is comparing noise.
func TestTheSameGraphIsDrawnTheSameWayTwice(t *testing.T) {
	g := star(7)
	if a, b := Draw(g, 80, 20, false), Draw(g, 80, 20, false); a != b {
		t.Errorf("two draws of one graph differ:\n%s\n---\n%s", a, b)
	}
}

// Every node is where the reader can see it: named in the picture, inside the
// width it was given.
func TestEveryNodeOfASmallGraphIsDrawnAndNamedWithinTheWidth(t *testing.T) {
	out := Draw(star(5), 80, 20, false)
	for _, want := range []string{"Kestrel", "Note 00", "Note 04"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not in the drawing:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if w := strWidth(line); w > 80 {
			t.Errorf("a %d-column line in an 80-column drawing: %q", w, line)
		}
	}
}

// A title is vault content written by someone else's agent. Drawn raw into a
// terminal, an ESC in it is an instruction the terminal carries out — colour
// at best, a rewritten screen or title bar at worst (invariant 6).
func TestATitleCarryingAnEscapeSequenceIsDrawnAsTextNotObeyed(t *testing.T) {
	g := star(2)
	g.Nodes[1].Title = "evil\x1b]0;pwned\x07\x1b[2J"
	if out := Draw(g, 80, 20, false); strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("a control character from a title reached the terminal: %q", out)
	}
}

// When there is not room to name every node, the drawing says how many went
// unnamed rather than showing a graph that looks smaller than it is.
func TestNodesLeftUnlabelledForSpaceAreCountedAndTheFocusIsNotOneOfThem(t *testing.T) {
	out := Draw(star(40), 40, 8, false)
	if !strings.Contains(out, "left unlabelled for space") {
		t.Errorf("a crowded drawing dropped names without saying so:\n%s", out)
	}
	if !strings.Contains(out, "Kestrel") {
		t.Errorf("the focus lost its name to a crowd of neighbours:\n%s", out)
	}
}

// Colour is for a terminal; asked for none, the drawing carries no escape
// codes at all.
func TestADrawingWithoutColourHasNoEscapeCodes(t *testing.T) {
	if out := Draw(star(3), 60, 12, false); strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes in an uncoloured drawing: %q", out)
	}
	if out := Draw(star(3), 60, 12, true); !strings.Contains(out, "\x1b[") {
		t.Error("a coloured drawing has no colour in it")
	}
}

// A wide script takes two columns a character; counting it as one pushed the
// label past the right edge and into the next node's name.
func TestAWideScriptLabelIsMeasuredInColumns(t *testing.T) {
	if got := Label(Node{Title: "日本語のとても長いノートのタイトル"}, 10); strWidth(got) > 10 {
		t.Errorf("a 10-column label came out %d columns: %q", strWidth(got), got)
	}
}

func TestEveryNodeIsLaidOutInsideTheBox(t *testing.T) {
	pos := Layout(star(12), 100, 40)
	if len(pos) != 13 {
		t.Fatalf("laid out %d of 13 nodes", len(pos))
	}
	for s, p := range pos {
		if p.X < 0 || p.X > 100 || p.Y < 0 || p.Y > 40 {
			t.Errorf("%s at %+v is outside the 100×40 box", s, p)
		}
	}
}
