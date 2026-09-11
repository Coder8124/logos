package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `brain index` is the command every checkpoint tells the user to run, and
// the plan for a vault two people share over git depends on .brain/ never
// reaching a commit — so this is where EnsureGitignore has to be wired in,
// not left as a package function nobody calls. The first run must both write
// the rule and say so; invariant 3 (CLAUDE.md) is that a feature announces
// itself, not that it just happens to work.
func TestBrainIndexAddsAndAnnouncesTheGitignoreRuleOnAFreshVault(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off")

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatalf("brain index: %v", err)
		}
	})
	if !strings.Contains(out, ".brain/") || !strings.Contains(out, ".gitignore") {
		t.Errorf("brain index did not announce writing the .gitignore rule:\n%s", out)
	}

	got, err := os.ReadFile(filepath.Join(vaultDir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(got), ".brain/") {
		t.Errorf(".gitignore does not exclude .brain/:\n%s", got)
	}
}

// A second run against the same vault must stay quiet about it — the rule is
// already there, so announcing it again on every single `brain index` would
// make the one useful signal (this run just changed your repo) indistinguishable
// from routine noise.
func TestBrainIndexStaysQuietAboutGitignoreOnceItIsAlreadyThere(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off")

	if err := runIndex(false); err != nil {
		t.Fatalf("first index: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatalf("second index: %v", err)
		}
	})
	if strings.Contains(out, ".gitignore") {
		t.Errorf("a second run re-announced the gitignore rule it already wrote:\n%s", out)
	}
}
