package graph

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/Coder8124/logos/internal/text"
	"github.com/Coder8124/logos/internal/untrusted"
)

// Draw renders g as a node-link picture for a terminal: edges traced in
// braille dots, notes as dots with their names beside them, the focus at the
// centre — the shape the app's graph view (and Obsidian's) draws, in a grid of
// characters. cols and rows are the size of the picture, not counting the
// header and footer lines. color turns on ANSI colour; off, the picture is the
// same and only the dimming of outer rings is lost.
//
// Every label is vault content and is drawn into a terminal, so it is stripped
// of control characters first: a title carrying ESC would otherwise be an
// escape sequence the terminal obeys (invariant 6), not text it shows.
func Draw(g Graph, cols, rows int, color bool) string {
	cols, rows = max(cols, 20), max(rows, 6)
	c := newCanvas(cols, rows)

	// Laid out in dot space, inset so a node on the edge of the box still has
	// a cell to sit in. Braille dots are close enough to square that the
	// picture keeps its proportions.
	dw, dh := float64(cols*2-4), float64(rows*4-4)
	pos := Layout(g, dw, dh)
	dot := func(slug string) (int, int, bool) {
		p, ok := pos[slug]
		if !ok {
			return 0, 0, false
		}
		return int(math.Round(p.X)) + 2, int(math.Round(p.Y)) + 2, true
	}

	// Similarity first and wikilinks last, so where two edges share a cell the
	// one a viewer can trust decides its colour.
	edges := append([]Edge(nil), g.Edges...)
	sort.SliceStable(edges, func(i, j int) bool { return edgeRank(edges[i]) < edgeRank(edges[j]) })
	for _, e := range edges {
		x0, y0, ok0 := dot(e.Src)
		x1, y1, ok1 := dot(e.Dst)
		if !ok0 || !ok1 {
			continue
		}
		c.line(x0, y0, x1, y1, edgeRank(e), e.Provenance == Similarity)
	}

	// Nodes and labels in order of importance — the focus, then the nearer
	// ring, then the better connected — so when labels compete for space the
	// ones a reader came for are the ones that get it.
	nodes := append([]Node(nil), g.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		if (nodes[i].Slug == g.Focus) != (nodes[j].Slug == g.Focus) {
			return nodes[i].Slug == g.Focus
		}
		if nodes[i].Hops != nodes[j].Hops {
			return nodes[i].Hops < nodes[j].Hops
		}
		if nodes[i].Degree != nodes[j].Degree {
			return nodes[i].Degree > nodes[j].Degree
		}
		return nodes[i].Slug < nodes[j].Slug
	})
	for _, n := range nodes {
		x, y, ok := dot(n.Slug)
		if !ok {
			continue
		}
		mark := '●'
		switch {
		case n.Slug == g.Focus:
			// Its own glyph, not just its own colour: under NO_COLOR or in a
			// pasted drawing the brass is gone and nothing else says where the
			// picture is centred.
			mark = '◉'
		case n.Kind == "missing":
			mark = '○'
		case n.Kind == "project":
			mark = '■'
		case n.Kind == "memory":
			mark = '◆'
		}
		// Two nodes in one cell: the one drawn first — the more important —
		// keeps it.
		if c.at(x/2, y/4) == 0 {
			c.put(x/2, y/4, mark, nodeStyle(n, g.Focus))
		}
	}
	hidden := 0
	for _, n := range nodes {
		x, y, ok := dot(n.Slug)
		if !ok {
			continue
		}
		// Widest first, shortened only as far as the space beside the node
		// forces: a fixed clip cut names that had a whole empty row to sit in.
		// The cap is what stops one long title walling off a row that later,
		// less important labels also need.
		width := 44
		if n.Slug == g.Focus {
			width = 60
		}
		placed := false
		for w := width; w >= minLabel && !placed; w-- {
			placed = c.label(x/2, y/4, Label(n, w), labelStyle(n, g.Focus))
		}
		if !placed {
			hidden++
		}
	}

	var b strings.Builder
	b.WriteString(c.render(color))
	b.WriteString("\n")
	// Only the kinds in this picture: a key naming shapes that are not on
	// screen is one more thing to read before the drawing.
	legend := "◉ focus  ● note"
	if hasKind(g, "checkpoint") {
		legend = "◉ focus  ● note or checkpoint"
	}
	for _, k := range []struct{ kind, text string }{
		{"project", "■ project"}, {"memory", "◆ memory"}, {"missing", "○ linked, not written yet"},
	} {
		if hasKind(g, k.kind) {
			legend += "  " + k.text
		}
	}
	if hasSimilarity(g) {
		legend += "  ⠒⠒ linked  ⠂⠂ similar, not linked"
	}
	b.WriteString(paint(color, styleDim, legend))
	b.WriteString("\n")
	if hidden > 0 {
		// Invariant 3: a picture that quietly drops names reads as a graph
		// with fewer notes in it than it has.
		b.WriteString(paint(color, styleDim, fmt.Sprintf(
			"%d %s left unlabelled for space — logos graph --list names every node\n",
			hidden, plural(hidden, "node", "nodes"))))
	}
	return b.String()
}

// minLabel is the narrowest a label is cut before the node goes unnamed
// instead: under it a name is a few letters and an ellipsis, which reads as
// noise, and the footer's count of unnamed nodes is the more honest answer.
const minLabel = 8

// Label is how a node is named in the drawing: its title, or the last segment
// of its slug, on one line with control characters removed and clipped to
// width columns.
func Label(n Node, width int) string {
	name := n.Title
	if strings.TrimSpace(name) == "" {
		name = trailing(n.Slug)
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, name)
	name = untrusted.Inline(name)
	for strWidth(name) > width {
		name = text.Ellipsize(name, len([]rune(name))-1)
	}
	return name
}

func hasKind(g Graph, kind string) bool {
	for _, n := range g.Nodes {
		if n.Kind == kind && n.Slug != g.Focus {
			return true
		}
	}
	return false
}

func hasSimilarity(g Graph) bool {
	for _, e := range g.Edges {
		if e.Provenance == Similarity {
			return true
		}
	}
	return false
}

func edgeRank(e Edge) int {
	switch e.Provenance {
	case Wikilink:
		return styleEdgeLink
	case Typed:
		return styleEdgeTyped
	default:
		return styleEdgeSimilar
	}
}

// Styles, ordered so that a higher value wins a shared cell.
const (
	styleNone = iota
	styleEdgeSimilar
	styleEdgeTyped
	styleEdgeLink
	styleDim
	styleOuter
	styleNear
	styleFocus
)

func nodeStyle(n Node, focus string) int {
	switch {
	case n.Slug == focus:
		return styleFocus
	case n.Kind == "missing" || n.Hops > 1:
		return styleOuter
	default:
		return styleNear
	}
}

func labelStyle(n Node, focus string) int { return nodeStyle(n, focus) }

// ANSI 256-colour codes: edges grey and faint, nearer rings brighter, the focus
// in brass — the dimming by distance that lets an eye find the centre.
var palette = map[int]string{
	styleEdgeSimilar: "38;5;237",
	styleEdgeTyped:   "38;5;240",
	styleEdgeLink:    "38;5;245",
	styleDim:         "38;5;242",
	styleOuter:       "38;5;244",
	styleNear:        "38;5;252",
	styleFocus:       "1;38;5;179",
}

func paint(color bool, style int, s string) string {
	if !color || style == styleNone {
		return s
	}
	return "\x1b[" + palette[style] + "m" + s + "\x1b[0m"
}

// canvas is a grid of character cells. A cell holds either braille dots (edges)
// or one rune of text (a node or a label), and text always wins: a name drawn
// through by an edge is unreadable, an edge with a gap under a name is not.
type canvas struct {
	cols, rows int
	dots       []uint8
	dotStyle   []int
	text       []rune
	textStyle  []int
}

func newCanvas(cols, rows int) *canvas {
	n := cols * rows
	return &canvas{cols: cols, rows: rows,
		dots: make([]uint8, n), dotStyle: make([]int, n),
		text: make([]rune, n), textStyle: make([]int, n)}
}

// brailleBit is the bit for dot (x%2, y%4) of a cell, per Unicode's braille
// block: dots 1–3 and 7 down the left column, 4–6 and 8 down the right.
var brailleBit = [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

func (c *canvas) plot(x, y, style int) {
	if x < 0 || y < 0 || x >= c.cols*2 || y >= c.rows*4 {
		return
	}
	i := (y/4)*c.cols + x/2
	c.dots[i] |= brailleBit[x%2][y%4]
	c.dotStyle[i] = max(c.dotStyle[i], style)
}

// line traces a Bresenham line in dots. A similarity edge is dotted — every
// third dot — so it reads as a suggestion even without colour.
func (c *canvas) line(x0, y0, x1, y1, style int, dotted bool) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	err := dx + dy
	for step := 0; ; step++ {
		if !dotted || step%3 == 0 {
			c.plot(x0, y0, style)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c *canvas) at(col, row int) rune {
	if col < 0 || row < 0 || col >= c.cols || row >= c.rows {
		return 0
	}
	return c.text[row*c.cols+col]
}

func (c *canvas) put(col, row int, r rune, style int) {
	if col < 0 || row < 0 || col >= c.cols || row >= c.rows {
		return
	}
	i := row*c.cols + col
	c.text[i], c.textStyle[i] = r, style
}

// label writes s beside the node at (col,row): right of it if it fits, else
// left. It will not write over another node or label, and reports false when
// neither side had room.
func (c *canvas) label(col, row int, s string, style int) bool {
	w := strWidth(s)
	if w == 0 {
		return true
	}
	for _, start := range []int{col + 2, col - 1 - w} {
		if c.free(start, row, w) {
			// A clear cell either side: an edge running into the first letter
			// made "●⠤bom" read as one smudge rather than a dot and its name.
			for _, pad := range []int{start - 1, start + w} {
				if c.at(pad, row) == 0 {
					c.put(pad, row, ' ', styleNone)
				}
			}
			x := start
			for _, r := range s {
				c.put(x, row, r, style)
				if runeWidth(r) == 2 {
					// The terminal draws a wide rune across two cells; the
					// second must be written as nothing, not as a dot.
					c.put(x+1, row, 0xFFFF, style)
				}
				x += runeWidth(r)
			}
			return true
		}
	}
	return false
}

// free is whether columns [start, start+w) of row, plus one cell of breathing
// room each side, hold no text. The gap is what keeps two labels on one row
// from reading as one name.
func (c *canvas) free(start, row, w int) bool {
	if start < 0 || start+w > c.cols || row < 0 || row >= c.rows {
		return false
	}
	for x := start - 1; x <= start+w; x++ {
		if x < 0 || x >= c.cols {
			continue
		}
		if c.text[row*c.cols+x] != 0 {
			return false
		}
	}
	return true
}

func (c *canvas) render(color bool) string {
	var b strings.Builder
	for row := 0; row < c.rows; row++ {
		var line strings.Builder
		style := styleNone
		run := strings.Builder{}
		flush := func() {
			line.WriteString(paint(color, style, run.String()))
			run.Reset()
		}
		for col := 0; col < c.cols; col++ {
			i := row*c.cols + col
			r, s := ' ', styleNone
			switch {
			case c.text[i] == 0xFFFF:
				continue
			case c.text[i] != 0:
				r, s = c.text[i], c.textStyle[i]
			case c.dots[i] != 0:
				r, s = rune(0x2800+int(c.dots[i])), c.dotStyle[i]
			}
			if s != style {
				flush()
				style = s
			}
			run.WriteRune(r)
		}
		flush()
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// runeWidth is how many terminal columns r takes: two for the East Asian wide
// scripts, one otherwise. Approximate, but a CJK title counted as one column
// per character pushed every label after it off its node.
func runeWidth(r rune) int {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || (r >= 0xFF01 && r <= 0xFF60) || (r >= 0x1F300 && r <= 0x1FAFF) {
		return 2
	}
	return 1
}

func strWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
