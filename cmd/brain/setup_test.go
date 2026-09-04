package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// --dry-run is the flag a careful person reaches for first, and it used to be
// the one that did the damage: it created the vault directory and then recorded
// it as this machine's vault. That recording is what the desktop app reads to
// find the vault at all — it is launched from Finder and inherits no
// BRAIN_VAULT — so previewing a setup against a scratch directory silently
// repointed the app away from the user's real memory, which then reported a
// healthy zero of everything.
func TestADryRunSetupNeitherCreatesTheVaultNorRepointsTheMachine(t *testing.T) {
	// A config directory of our own, so the real pointer is never touched.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	before := vault.Recorded()

	scratch := filepath.Join(t.TempDir(), "try-it")
	dir, created, err := chooseVault([]string{"--vault", scratch}, true)
	if err != nil {
		t.Fatal(err)
	}

	if dir != scratch {
		t.Errorf("chooseVault returned %q, want the path that was asked for", dir)
	}
	if !created {
		t.Error("created = false; a dry run still has to report that the directory is missing")
	}
	if _, err := os.Stat(scratch); err == nil {
		t.Errorf("--dry-run created %s", scratch)
	}
	if now := vault.Recorded(); now != before {
		t.Errorf("--dry-run repointed this machine's vault from %q to %q", before, now)
	}
}

// The other half: a real setup must still do both, or the desktop app never
// finds a vault that is not at the default.
func TestARealSetupCreatesTheVaultAndRecordsIt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	scratch := filepath.Join(t.TempDir(), "for-real")
	dir, created, err := chooseVault([]string{"--vault", scratch}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false for a directory that did not exist")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("setup did not create the vault: %v", err)
	}
	// Private from the first mkdir, not tightened later.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("new vault is mode %v; it holds every note and must not be readable by other accounts", perm)
	}
	if got := vault.Recorded(); got != dir {
		t.Errorf("recorded vault = %q, want %q — the desktop app reads this", got, dir)
	}
}

// "last checkpoint 1 minutes ago" is the kind of line a reviewer notices before
// anything else. This copy of internal/health's formatter dropped the plural
// when it was made.
func TestContinuityDoesNotSayOneMinutes(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{90 * time.Second, "1 minute"},
		{5 * time.Minute, "5 minutes"},
		{90 * time.Minute, "1 hour"},
		{5 * time.Hour, "5 hours"},
		{36 * time.Hour, "36 hours"},
		{50 * time.Hour, "2 days"},
	} {
		if got := roughAge(now.Add(-tc.ago).Unix()); got != tc.want {
			t.Errorf("roughAge(%s ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}
