package contextpack

import (
	"strings"
	"testing"
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
