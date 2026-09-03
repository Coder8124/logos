package contextpack

import (
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

func TestSpenderStopsAtTheAllowance(t *testing.T) {
	sp := newSpender(1000)
	// secNotes gets 35% of 1000 = 350 tokens. Each chunk is 400 chars ≈ 101.
	chunks := make([]string, 10)
	for i := range chunks {
		chunks[i] = strings.Repeat("x", 400)
	}
	kept := sp.take(secNotes, chunks)

	if len(kept) == 0 || len(kept) >= len(chunks) {
		t.Fatalf("budget should admit some chunks and refuse others, kept %d of %d", len(kept), len(chunks))
	}
	if sp.lines[0].Dropped != len(chunks)-len(kept) {
		t.Errorf("dropped count wrong: %+v", sp.lines[0])
	}
}

func TestUnspentAllowanceCarriesForward(t *testing.T) {
	// A project with no checkpoint should not return a thinner pack than one
	// that has a short checkpoint — the unused share belongs to what follows.
	lean := newSpender(1000)
	lean.take(secCheckpoint, nil)
	withCarry := lean.allowance(secWorking)

	fresh := newSpender(1000)
	bare := fresh.allowance(secWorking)

	if withCarry <= bare {
		t.Errorf("unspent checkpoint budget should carry forward: %d vs %d", withCarry, bare)
	}
}

func TestFirstItemIsAlwaysAdmitted(t *testing.T) {
	// A heading with nothing under it reads like a bug. One oversized item is
	// the lesser evil.
	sp := newSpender(10)
	kept := sp.take(secNotes, []string{strings.Repeat("x", 5000)})
	if len(kept) != 1 {
		t.Errorf("want the first item admitted regardless of size, kept %d", len(kept))
	}
}

func TestFitTrimsOnALineBoundary(t *testing.T) {
	text := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\ntheta"
	got, trimmed := fit(text, 3)
	if !trimmed {
		t.Fatal("want trimmed")
	}
	if !strings.Contains(got, "trimmed to fit") {
		t.Errorf("a trimmed body must say so, got %q", got)
	}
	if strings.Contains(got, "theta") {
		t.Errorf("trim did not actually cut: %q", got)
	}
	// Cut on a newline, never mid-word.
	head := strings.Split(got, "\n\n_[")[0]
	for _, line := range strings.Split(head, "\n") {
		if line != "" && !strings.Contains(text, line) {
			t.Errorf("produced a partial line %q", line)
		}
	}
}

func TestFitLeavesSmallTextAlone(t *testing.T) {
	got, trimmed := fit("short", 100)
	if trimmed || got != "short" {
		t.Errorf("fit(%q) = %q, %v — want it untouched", "short", got, trimmed)
	}
}

// An absent section must hand its share to the sections that follow, not
// forfeit it. Getting this wrong produced packs that spent half their budget
// while still reporting notes they had dropped — the worst of both.
func TestSkippedSectionReleasesItsShare(t *testing.T) {
	skipped := newSpender(1000)
	skipped.skip(secCheckpoint)
	after := skipped.allowance(secWorking)

	fresh := newSpender(1000)
	bare := fresh.allowance(secWorking)

	if after <= bare {
		t.Errorf("skip should release the share: %d vs %d", after, bare)
	}
	if want := int(shares[secCheckpoint]*1000) + bare; after != want {
		t.Errorf("allowance after skip = %d, want %d", after, want)
	}
}

// A pin is a promise: it must survive a ranked memory that would otherwise
// outrank it, and it must appear even when the memories section's whole
// allowance is tiny — the entire point of pinning is that relevance and
// budget pressure do not get a vote.
func TestPinnedMemoriesAreNeverDroppedForBudget(t *testing.T) {
	p := &Pack{}
	p.Pinned = []memory.Memory{{ID: 1, Kind: memory.Fact, Text: "always show this", Source: "manual"}}
	for i := 2; i < 40; i++ {
		p.Related = append(p.Related, memory.Memory{
			ID: int64(i), Kind: memory.Fact, Source: "conversation",
			Text: strings.Repeat("x", 200), // large enough to fill the section alone
		})
	}

	sp := newSpender(200) // a small total budget, so secMemories' share is tiny
	kept := p.spendMemories(sp)

	found := false
	for _, s := range kept {
		if strings.Contains(s, "always show this") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a pinned memory must never be dropped for budget, kept: %v", kept)
	}
}

// If pinned items alone exceed the section's whole share, the documented
// behaviour is that the ranked tail gets nothing this pass — not a half-cut
// pinned item, not an undefined truncation.
func TestPinnedMemoriesThatExceedTheBudgetStillAllRender(t *testing.T) {
	p := &Pack{}
	for i := 0; i < 5; i++ {
		p.Pinned = append(p.Pinned, memory.Memory{
			ID: int64(i + 1), Kind: memory.Fact, Source: "manual",
			Text: strings.Repeat("pinned content ", 50),
		})
	}
	p.Related = []memory.Memory{{ID: 99, Kind: memory.Fact, Source: "conversation", Text: "a ranked fact"}}

	sp := newSpender(100) // far too small to hold five long pinned entries
	kept := p.spendMemories(sp)

	if len(kept) != len(p.Pinned) {
		t.Fatalf("all pinned memories should render even over budget, got %d of %d", len(kept), len(p.Pinned))
	}
	for _, s := range kept {
		if strings.Contains(s, "a ranked fact") {
			t.Error("the ranked tail should get no room when pinned items alone exceed the allowance")
		}
	}
	// The ranked drop has to be visible, or the budget looks fine when it is
	// not — the same rule renderBudget enforces for every other section.
	if len(p.Excluded) == 0 {
		t.Error("dropping the ranked tail entirely should still be reported in Excluded")
	}
}

// A memory that is both pinned and would also have ranked must render once,
// tagged as pinned — not twice.
func TestPinnedMemoryIsNotDuplicatedInTheRankedTail(t *testing.T) {
	p := &Pack{}
	m := memory.Memory{ID: 7, Kind: memory.Preference, Text: "short replies please", Source: "manual"}
	p.Pinned = []memory.Memory{m}
	p.Preferences = []memory.Memory{m}

	sp := newSpender(4000)
	kept := p.spendMemories(sp)

	count := 0
	for _, s := range kept {
		if strings.Contains(s, "short replies please") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("a pinned-and-ranked memory should render once, rendered %d times: %v", count, kept)
	}
}
