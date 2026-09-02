package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/Coder8124/brain/internal/graph"
)

// runGraph prints the ego-graph as text — the CLI counterpart to the app's
// memory-graph view, useful for checking what the visual will render.
func runGraph(focus string, hops int, similarity bool) error {
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()

	if focus == "" {
		focus = graph.DefaultFocus(ix.DB, "daily/"+time.Now().Format("2006-01-02"))
	}
	g, err := graph.Ego(ix.DB, focus, hops, similarity)
	if err != nil {
		return err
	}
	if len(g.Nodes) == 0 {
		return fmt.Errorf("no node matches %q — nothing to graph", focus)
	}

	fmt.Printf("● %s  (%d nodes, %d edges, %d hops)\n\n", g.Focus, len(g.Nodes), len(g.Edges), hops)

	// Nodes, closest hop first, then by degree.
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Hops != g.Nodes[j].Hops {
			return g.Nodes[i].Hops < g.Nodes[j].Hops
		}
		return g.Nodes[i].Degree > g.Nodes[j].Degree
	})
	fmt.Println("nodes")
	for _, n := range g.Nodes {
		fmt.Printf("  %s %-28s %-9s deg %d\n", ringMark(n.Hops), n.Slug, n.Kind, n.Degree)
	}

	fmt.Println("\nedges")
	for _, e := range g.Edges {
		fmt.Printf("  %s %s —%s→ %s  (%.2f)\n", provMark(e.Provenance), e.Src, e.Pred, e.Dst, e.Conf)
	}
	return nil
}

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
