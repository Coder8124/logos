package graph

import (
	"fmt"
	"strings"
	"testing"
)

func projectWithCheckpoints(n int) Graph {
	g := Graph{Focus: "projects/brain"}
	g.Nodes = append(g.Nodes, Node{Slug: "projects/brain", Title: "brain", Kind: "project"})
	for i := range n {
		slug := fmt.Sprintf("sessions/brain/202609%02d-120000-claude", i+1)
		g.Nodes = append(g.Nodes, Node{Slug: slug, Title: fmt.Sprintf("brain — task %02d", i+1), Kind: "checkpoint", Hops: 1})
		g.Edges = append(g.Edges, Edge{Src: slug, Dst: "projects/brain", Pred: "checkpoint_of", Provenance: Typed})
	}
	g.Nodes = append(g.Nodes, Node{Slug: "memory:1", Title: "a fact", Kind: "memory", Hops: 1})
	g.Edges = append(g.Edges, Edge{Src: "memory:1", Dst: "projects/brain", Pred: "about", Provenance: Typed})
	return g
}

// A project's whole history drawn at once is forty dots around a hub with no
// room for a name: the recent work is what a reader opens the graph for, and
// the rest is counted rather than drawn.
func TestAProjectIsDrawnWithItsRecentCheckpointsAndTheRestAreCounted(t *testing.T) {
	g, left := Condense(projectWithCheckpoints(30), 10)
	if left != 20 {
		t.Errorf("left out %d checkpoints, want 20", left)
	}
	have := map[string]bool{}
	for _, n := range g.Nodes {
		have[n.Slug] = true
	}
	if !have["sessions/brain/20260930-120000-claude"] || have["sessions/brain/20260901-120000-claude"] {
		t.Errorf("kept the wrong end of the history: %v", have)
	}
	if !have["memory:1"] || !have["projects/brain"] {
		t.Errorf("condensing dropped something that is not a checkpoint: %v", have)
	}
	for _, e := range g.Edges {
		if !have[e.Src] || !have[e.Dst] {
			t.Errorf("an edge to a node no longer drawn: %+v", e)
		}
	}
}

// Every checkpoint of brain is titled "brain — …"; around brain's own hub the
// prefix is the same eight columns on every label, taken from the task.
func TestAroundAProjectItsNameIsNotRepeatedOnEveryCheckpoint(t *testing.T) {
	g, _ := Condense(projectWithCheckpoints(3), 10)
	for _, n := range g.Nodes {
		if n.Kind == "checkpoint" && strings.HasPrefix(n.Title, "brain — ") {
			t.Errorf("%q still carries the project's name", n.Title)
		}
	}
}
