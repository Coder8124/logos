package main

import (
	"fmt"
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

// A pinned procedure never enters a context pack, so Pinned leaves it out —
// and the recent list skipped every pinned memory, so the page dropped it
// without a word.
func TestShowListsAPinnedProcedureAmongTheProjectsMemories(t *testing.T) {
	standIn(t, "elsewhere")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	m := &memory.Memory{Text: "route: run migrations with --lock | trap: two deploys race | verify: logos doctor", Kind: memory.Procedure, Source: "manual", Project: "shop"}
	if _, err := memory.Store(ix.DB, nil, "", m); err != nil {
		t.Fatal(err)
	}
	if err := memory.Pin(ix.DB, m.ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runShow([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "run migrations with --lock") {
		t.Errorf("the pinned procedure is missing from the page:\n%s", out)
	}
}

// Decay ranked an unused memory down and told nobody, so a fact that drifted
// out of every pack looked like one Logos had lost. The page names what has
// gone unrecalled, by id, with the two answers to it — and leaves alone what
// was recalled lately or pinned.
func TestShowNamesTheMemoriesNotRecalledInSixtyDays(t *testing.T) {
	standIn(t, "elsewhere")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	days := func(n int) int64 { return time.Now().AddDate(0, 0, -n).Unix() }
	stored := map[string]*memory.Memory{}
	for _, m := range []*memory.Memory{
		{Text: "The staging box is called kestrel.", Kind: memory.Fact, Source: "mcp", Project: "shop", Created: days(100)},
		{Text: "Invoices are generated nightly.", Kind: memory.Fact, Source: "mcp", Project: "shop", Created: days(200)},
		{Text: "Refunds go through the ledger service.", Kind: memory.Fact, Source: "mcp", Project: "shop", Created: days(150)},
		{Text: "Checkout must stay under 200ms at p95.", Kind: memory.Fact, Source: "mcp", Project: "shop", Created: days(90)},
		{Text: "The tax table is owned by finance.", Kind: memory.Fact, Source: "mcp", Project: "shop", Created: days(5)},
	} {
		if _, err := memory.Store(ix.DB, nil, "", m); err != nil {
			t.Fatal(err)
		}
		stored[m.Text] = m
	}
	setUsed := func(text string, at int64) {
		if _, err := ix.DB.Exec("UPDATE memories SET last_used = ?, uses = 1 WHERE id = ?", at, stored[text].ID); err != nil {
			t.Fatal(err)
		}
	}
	setUsed("Invoices are generated nightly.", days(70))
	setUsed("Refunds go through the ledger service.", days(10))
	if err := memory.Pin(ix.DB, stored["Checkout must stay under 200ms at p95."].ID); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runShow([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	_, section, ok := strings.Cut(out, "Fading — 2 memories not recalled in 60 days")
	if !ok {
		t.Fatalf("the page has no fading section counting 2:\n%s", out)
	}
	for _, want := range []string{
		fmt.Sprintf("#%d fact: The staging box is called kestrel. (never recalled, written 100d ago)", stored["The staging box is called kestrel."].ID),
		fmt.Sprintf("#%d fact: Invoices are generated nightly. (last recalled 70d ago)", stored["Invoices are generated nightly."].ID),
		"logos memory pin <id>",
		"logos memory forget <id>",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the fading section is missing %q:\n%s", want, section)
		}
	}
	if strings.Index(section, "kestrel") > strings.Index(section, "Invoices") {
		t.Errorf("the longest untouched memory is not listed first:\n%s", section)
	}
	for _, not := range []string{"ledger service", "200ms", "tax table"} {
		if strings.Contains(section, not) {
			t.Errorf("%q was listed as fading though it was recalled lately, pinned, or is new:\n%s", not, section)
		}
	}
}
