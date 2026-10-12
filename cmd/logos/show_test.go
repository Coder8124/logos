package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// resume is addressed to a model; a person who wanted to see where a project
// stood had to read a context pack. show lays the same record out for them —
// and, since a person looking is not an agent using, counts nothing as used.
func TestShowLaysOutAProjectForAPersonAndCountsNothingAsUsed(t *testing.T) {
	standIn(t, "elsewhere")
	commitRulings(t, &session.Checkpoint{
		Project: "shop", Agent: "codex", Task: "speed up checkout", Next: "profile the tax lookup",
		TS:       time.Now().Add(-2 * time.Hour).Unix(),
		Verified: []string{"go test ./cart passes"},
		Blockers: []string{"the staging database is down"},
		Failed:   []string{"caching the cart — internal/cart/cache.go evicts on every write"},
	})
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AddNote(ix.DB, "shop", "claude-code", "tried batching the tax calls"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*memory.Memory{
		{Text: "Checkout must stay under 200ms at p95.", Kind: memory.Fact, Source: "mcp", Project: "shop"},
		// A memory is vault text another agent wrote: a terminal escape in it
		// must reach the page as text, not as an instruction to the terminal.
		{Text: "Tax rates come from \x1b]0;pwned\x07the finance export.", Kind: memory.Fact, Source: "mcp", Project: "shop"},
		{Text: "Prices are in cents.", Kind: memory.Fact, Source: "mcp", Project: "shop", Quarantined: true},
	} {
		if _, err := memory.Store(ix.DB, nil, "", m); err != nil {
			t.Fatal(err)
		}
	}
	before := totalUses(t)
	ix.Close()

	out := captureStdout(t, func() {
		if err := runShow([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"Last checkpoint — codex, 2h ago",
		"doing: speed up checkout",
		"next:  profile the tax lookup",
		"1 verified · 1 blocker",
		"✗ the staging database is down",
		"Not yet checkpointed — 1 note",
		"tried batching the tax calls",
		"Ruled out — 1 approach",
		"caching the cart",
		"Checkout must stay under 200ms",
		"1 memory is waiting for your review — logos review",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page is missing %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("a control byte from vault text reached the terminal:\n%q", out)
	}
	if strings.Contains(out, "Prices are in cents") {
		t.Errorf("a memory still waiting for review was shown as one Logos knows:\n%s", out)
	}
	if after := totalUses(t); after != before {
		t.Errorf("showing the page counted memories as used: uses went from %d to %d", before, after)
	}
}

// A name with nothing behind it gets the same answer resume gives, not an
// empty page that looks like a project with nothing in it.
func TestShowOnAProjectWithNoRecordSaysSo(t *testing.T) {
	standIn(t, "elsewhere")
	commitRulings(t, &session.Checkpoint{Project: "shop", Task: "speed up checkout"})
	out := captureStdout(t, func() {
		if err := runShow([]string{"shoe"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `nothing recorded for "shoe"`) || !strings.Contains(out, "shop") {
		t.Errorf("show on an unknown project did not say so:\n%s", out)
	}
}

func totalUses(t *testing.T) int {
	t.Helper()
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var n int
	if err := ix.DB.QueryRow("SELECT COALESCE(SUM(uses), 0) FROM memories").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
