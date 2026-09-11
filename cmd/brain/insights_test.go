package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// brain insights must announce a count (invariant 3) even when the vault has
// nothing to say — a bare "no insights" without a number reads the same as a
// command that silently failed to look.
func TestBrainInsightsAnnouncesZeroWithANumberOnAnEmptyVault(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runInsights(nil); err != nil {
			t.Fatalf("runInsights returned an error: %v", err)
		}
	})
	if !strings.Contains(out, "0 insight") {
		t.Fatalf("expected the zero count spelled out, got:\n%s", out)
	}
}

// A recurring blocker committed straight to the vault (no index step) must
// still surface — brain insights reads checkpoints, not the cache, so a fresh
// clone with an empty .brain/index.db still finds it.
func TestBrainInsightsReportsARecurringBlockerWithItsCheckpointCited(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "first",
		Blockers: []string{"the vendor has not shipped the connector firmware"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "second",
		Blockers: []string{"still waiting on the connector firmware from the vendor"},
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := runInsights([]string{"kestrel-one"}); err != nil {
			t.Fatalf("runInsights returned an error: %v", err)
		}
	})
	if !strings.Contains(out, "connector firmware") {
		t.Fatalf("expected the recurring blocker's text in the output, got:\n%s", out)
	}
	if !strings.Contains(out, "sessions/kestrel-one/") {
		t.Fatalf("expected a checkpoint source cited in the output, got:\n%s", out)
	}
}

// The mechanical-only degradation notice (invariant 3 again) must reach the
// terminal, not just live as an unread constant in internal/insight.
func TestBrainInsightsPrintsTheDegradationNotice(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runInsights(nil); err != nil {
			t.Fatalf("runInsights returned an error: %v", err)
		}
	})
	if !strings.Contains(out, "mechanical") {
		t.Fatalf("expected the mechanical-tier notice in the output, got:\n%s", out)
	}
}

// --rules with no vault must fail the same way every other vault-reading verb
// does — brain insights is not exempt from the shared missingVaultError.
func TestBrainInsightsWithNoVaultReportsTheStandardMissingVaultError(t *testing.T) {
	vault := t.TempDir()
	os.RemoveAll(vault)
	t.Setenv("BRAIN_VAULT", vault)

	err := runInsights(nil)
	if err == nil {
		t.Fatal("expected an error for a vault that does not exist")
	}
}
