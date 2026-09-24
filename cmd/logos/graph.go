package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"golang.org/x/term"

	"github.com/Coder8124/logos/internal/graph"
)

// runGraph draws the ego-graph in the terminal — the CLI counterpart to the
// app's memory-graph view — or, with list, prints its nodes and edges as text,
// which is what a script or a diff wants and what names every node the drawing
// had no room to label.
func runGraph(focus string, hops int, similarity, list bool) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	// A name is resolved the way a user means it: a note by its slug or its
	// last segment, or a project by name. Asked for nothing, the project of
	// the directory they are standing in, which in a repository is the
	// question — and only then the vault's busiest note.
	if focus == "" {
		if p := projectHere(); p != "" {
			if slug, ok := graph.Resolve(ix.DB, p); ok {
				focus = slug
			}
		}
		if focus == "" {
			focus = graph.DefaultFocus(ix.DB, "daily/"+time.Now().Format("2006-01-02"))
		}
	} else if slug, ok := graph.Resolve(ix.DB, focus); ok {
		focus = slug
	}
	g, err := graph.Around(ix.DB, focus, hops, graph.Options{Similarity: similarity, Memories: true})
	if err != nil {
		return err
	}
	// Ego draws a focus the vault does not hold as a ghost node, which is right
	// for a wikilink target and wrong for the thing asked about: `graph
	// nonexistent` and an empty vault both drew "missing deg 0" and exited 0.
	if len(g.Nodes) == 0 || (len(g.Nodes) == 1 && len(g.Edges) == 0 && g.Nodes[0].Kind == "missing") {
		return fmt.Errorf("no note or entity %q in the vault — nothing to graph", focus)
	}

	fmt.Printf("◉ %s  (%d nodes, %d edges, %d hops)\n\n", graph.Label(graph.Node{Slug: g.Focus}, 200), len(g.Nodes), len(g.Edges), hops)
	if !list {
		drawn, older := graph.Condense(g, recentCheckpoints)
		cols, rows, color := drawSize(len(drawn.Nodes))
		fmt.Print(graph.Draw(drawn, cols, rows, color))
		if older > 0 {
			// Invariant 3: the picture is the recent end of the project, and
			// says so, rather than passing for all of it.
			fmt.Printf("%d older %s not drawn — logos graph --list names every node\n",
				older, map[bool]string{true: "checkpoint", false: "checkpoints"}[older == 1])
		}
		return nil
	}

	// Nodes, closest hop first, then by degree.
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Hops != g.Nodes[j].Hops {
			return g.Nodes[i].Hops < g.Nodes[j].Hops
		}
		return g.Nodes[i].Degree > g.Nodes[j].Degree
	})
	fmt.Println("nodes")
	for _, n := range g.Nodes {
		// A memory's slug is a number; its text is what names it.
		name := n.Slug
		if n.Kind == "memory" {
			name += " " + graph.Label(n, 60)
		}
		fmt.Printf("  %s %-28s %-9s deg %d\n", ringMark(n.Hops), name, n.Kind, n.Degree)
	}

	fmt.Println("\nedges")
	for _, e := range g.Edges {
		fmt.Printf("  %s %s —%s→ %s  (%.2f)\n", provMark(e.Provenance), e.Src, e.Pred, e.Dst, e.Conf)
	}
	return nil
}

// recentCheckpoints is how many of a project's checkpoints the drawing shows:
// about as many names as fit around a hub in an 80-column terminal.
const recentCheckpoints = 10

func ringMark(hops int) string {
	switch hops {
	case 0:
		return "◉"
	case 1:
		return "○"
	default:
		return "·"
	}
}

func provMark(p graph.Provenance) string {
	switch p {
	case graph.Wikilink:
		return "═" // solid
	case graph.Typed:
		return "─" // by confidence
	default:
		return "┄" // similarity lens
	}
}

// drawSize fits the drawing to the terminal: its full width up to a point past
// which lines only get longer, and enough rows for the nodes to spread without
// scrolling the header off a laptop screen. Colour only when the output is a
// terminal and NO_COLOR is unset — escape codes in a file or a pipe are noise
// in whatever reads it.
func drawSize(nodes int) (cols, rows int, color bool) {
	cols, rows = 100, 30
	fd := int(os.Stdout.Fd())
	tty := term.IsTerminal(fd)
	if tty {
		if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
			cols, rows = w, h
		}
	}
	cols = min(max(cols-1, 40), 180)
	rows = min(max(rows-6, 10), 8+nodes*2, 50)
	rows = max(rows, 10)
	return cols, rows, tty && os.Getenv("NO_COLOR") == ""
}
