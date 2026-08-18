package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
)

// A miniature vault with the shape that matters: a project note that links to a
// constraint note without repeating its words. Retrieval on "cost" will find
// bom-cost; only the edge finds yield-rate. That gap is the reason graph
// expansion is in the pack at all.
func seedVault(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()

	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/kestrel-one.md", `---
type: project
title: Kestrel One
relations:
  - { pred: depends_on, obj: "[[yield-rate]]", conf: 1.0, src: stated }
---
Smart glasses. Target BOM $118, actual $141.20. See [[bom-cost]].
`)
	write("topics/bom-cost.md", `---
type: topic
title: BOM cost
---
The bill of materials sits at $141.20 against a $118 target.
`)
	write("topics/yield-rate.md", `---
type: topic
title: Bonding yield
---
Display bonding runs at 71 percent first-pass. Every scrapped unit
is absorbed by the units that ship.
`)

	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	return ix
}

// The pack must reach a note that the query's own words would never surface,
// because the user's link says it is relevant. This is the case ranked
// retrieval structurally cannot cover.
func TestGraphReachPullsInALinkedNoteRetrievalWouldMiss(t *testing.T) {
	ix := seedVault(t)

	// Embedding is nil, so HybridSearch is skipped entirely: whatever appears
	// here arrived purely through the graph.
	p, err := Build(ix, nil, "", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Project == nil {
		t.Fatal("project note should have resolved")
	}

	var found bool
	for _, h := range p.Notes {
		if strings.Contains(h.Slug, "yield-rate") {
			found = true
			if h.Via == "" {
				t.Error("a graph-reached note must be marked as such, or it reads as a direct match")
			}
		}
	}
	if !found {
		t.Errorf("yield-rate should arrive through depends_on, got %v", slugs(p.Notes))
	}

	out := p.Render()
	if !strings.Contains(out, "reached through the graph") {
		t.Errorf("provenance for graph hits is missing from the render:\n%s", out)
	}
	if !strings.Contains(out, "71 percent") {
		t.Errorf("the pack must carry note *content*, not just titles:\n%s", out)
	}
}

// The checkpoint leads the pack and carries the field that stops repeated work.
func TestCheckpointLeadsTheRenderedPack(t *testing.T) {
	ix := seedVault(t)

	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task:   "cut the BOM",
		Failed: []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:   "quote the single-mic line",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint == nil {
		t.Fatal("checkpoint not picked up")
	}
	out := p.Render()

	if !strings.Contains(out, "Already tried, didn't work") {
		t.Errorf("the failed-approaches section must survive rendering:\n%s", out)
	}
	if i, j := strings.Index(out, "Where we left off"), strings.Index(out, "From the vault"); i < 0 || (j >= 0 && i > j) {
		t.Error("the checkpoint should lead — it is the one section nothing else can reconstruct")
	}
	if !strings.Contains(out, p.Checkpoint.Slug) {
		t.Errorf("the checkpoint should be cited in sources:\n%s", out)
	}
}

// A smaller budget must produce a smaller pack and say what it dropped, rather
// than silently returning less.
func TestSmallerBudgetDropsAndReportsIt(t *testing.T) {
	ix := seedVault(t)

	big, _ := Build(ix, nil, "", Request{Task: "reduce cost", Hint: "kestrel-one", Budget: 4000})
	small, _ := Build(ix, nil, "", Request{Task: "reduce cost", Hint: "kestrel-one", Budget: 120})

	bigOut, smallOut := big.Render(), small.Render()
	if len(smallOut) >= len(bigOut) {
		t.Errorf("a 120-token budget produced %d chars vs %d at 4000", len(smallOut), len(bigOut))
	}
	if small.Budget.Spent > big.Budget.Spent {
		t.Errorf("spend should track the ceiling: %d vs %d", small.Budget.Spent, big.Budget.Spent)
	}
	if len(small.Excluded) == 0 {
		t.Error("dropping content silently is the failure mode this field exists to prevent")
	}
	if !strings.Contains(smallOut, "Left out for space") {
		t.Errorf("exclusions must be visible to the agent:\n%s", smallOut)
	}
}

// Working notes are the uncommitted half of continuity and must be labelled as
// such — an agent should know they were never written down properly.
func TestUncommittedNotesAppearSeparately(t *testing.T) {
	ix := seedVault(t)
	if _, err := session.AddNote(ix.DB, "kestrel-one", "claude", "supplier call moved to Thursday"); err != nil {
		t.Fatal(err)
	}

	p, _ := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	out := p.Render()
	if !strings.Contains(out, "not yet checkpointed") {
		t.Errorf("uncommitted work must be marked uncommitted:\n%s", out)
	}
	if !strings.Contains(out, "supplier call moved") {
		t.Errorf("working note missing:\n%s", out)
	}
}

// Open commitments touching the project sort ahead of unrelated ones.
func TestProjectLoopsSortFirst(t *testing.T) {
	ix := seedVault(t)
	secretary.Add(ix.DB, &secretary.Commitment{Text: "renew the parking permit"})
	secretary.Add(ix.DB, &secretary.Commitment{Text: "send Kestrel One tooling PO"})

	p, _ := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if len(p.OpenLoops) < 2 {
		t.Fatalf("want both loops, got %d", len(p.OpenLoops))
	}
	if !strings.Contains(p.OpenLoops[0].Text, "tooling PO") {
		t.Errorf("the project's own loop should lead, got %q", p.OpenLoops[0].Text)
	}
}

// Asking about something unknown returns a thin pack, never an error. A memory
// layer that errors on an unfamiliar question is one an agent learns to avoid.
func TestUnknownSubjectDegradesInsteadOfFailing(t *testing.T) {
	ix := seedVault(t)

	p, err := Build(ix, nil, "", Request{Task: "something entirely unrelated to this vault"})
	if err != nil {
		t.Fatalf("build should degrade, not fail: %v", err)
	}
	if out := p.Render(); !strings.Contains(out, "No project matched") {
		t.Errorf("want an honest empty result:\n%s", out)
	}
}

// The task alone should find the project when no hint is given — an agent that
// says what it is doing has named the project without knowing it.
func TestProjectResolvesFromTheTaskText(t *testing.T) {
	ix := seedVault(t)

	p, _ := Build(ix, nil, "", Request{Task: "keep working on Kestrel One's cost problem"})
	if p.Project == nil || p.Project.Slug != "projects/kestrel-one" {
		t.Errorf("want the project resolved from the task, got %v", p.Project)
	}
}

func slugs(hits []index.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Slug)
	}
	return out
}
