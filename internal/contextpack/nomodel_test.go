package contextpack

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// A machine with no model runtime is the default state of a fresh install, not
// an edge case. The pack guarded both retrieval arms on a non-nil embedder,
// though both callees rank by keyword without one — so `remember` wrote the
// fact, `recall` found it, and every `context` and `resume` on that machine
// silently carried no memories at all. The project here has checkpoints-only
// history and no note, as most do, so the note's own fact list cannot carry it.
func TestAMemoryReachesThePackWithNoModelRuntime(t *testing.T) {
	ix := seedVault(t)
	m := memory.Memory{
		Text:    "The staging database is reset every Sunday at 03:00 UTC",
		Kind:    memory.Fact,
		Project: "shop",
		Source:  "test",
	}
	if _, err := memory.Store(ix.DB, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "when does the staging database reset", Hint: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Render(), "reset every Sunday") {
		t.Errorf("a memory recall can find is missing from the pack when no embedder is configured:\n%s", p.Render())
	}
}

// The vault-prose arm had the same guard. HybridSearch already falls back to
// the FTS arm for a nil provider; the pack simply never asked it. The hint
// names no project note, so graph reach cannot supply the note instead.
func TestAVaultNoteReachesThePackWithNoModelRuntime(t *testing.T) {
	ix := seedVault(t)

	p, err := Build(ix, nil, "", Request{Task: "bill of materials cost"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, h := range p.Notes {
		if h.Slug == "topics/bom-cost" {
			found = true
		}
	}
	if !found {
		t.Errorf("a note keyword search finds is missing from the pack when no embedder is configured; notes: %+v", p.Notes)
	}
}
