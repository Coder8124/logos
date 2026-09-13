package main

import (
	"path/filepath"
	"testing"

	"github.com/Coder8124/brain/internal/vault"
)

// `setup --vault /tmp/try-logos` recorded the directory as this machine's vault
// and said so cheerfully; the next `brain doctor` failed it as a temporary
// directory. Setup is where the check prevents the problem, so it asks there.
func TestSetupDoesNotRecordATemporaryVaultWithoutBeingTold(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("BRAIN_VAULT", "")
	withAnswers(t, "")

	scratch := filepath.Join(t.TempDir(), "try-logos")
	var rec recordOutcome
	captureStdout(t, func() {
		var err error
		_, _, rec, err = chooseVault([]string{"--vault", scratch}, false)
		if err != nil {
			t.Fatal(err)
		}
	})
	if got := vault.Recorded(); got != "" {
		t.Errorf("a temporary directory was recorded as this machine's vault: %q", got)
	}
	if rec != recordSkipTemp {
		t.Errorf("record outcome = %v, want recordSkipTemp so setup says why nothing was recorded", rec)
	}
}

// Declining is the default, not the only answer: --yes records it, for the
// person who really does want a throwaway vault to be the machine's.
func TestSetupRecordsATemporaryVaultWithYes(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("BRAIN_VAULT", "")

	scratch := filepath.Join(t.TempDir(), "try-logos")
	captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", scratch, "--yes"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if got := vault.Recorded(); got != scratch {
		t.Errorf("recorded vault = %q, want %q after --yes", got, scratch)
	}
}
