package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/vault"
)

// recordedVaultWithWork records a vault holding one project's checkpoints, the
// thing that makes moving the pointer expensive rather than routine.
func recordedVaultWithWork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, session.CheckpointDir, "kestrel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The pointer is one file, and `logos setup --vault B` rewrote it whether or
// not A held every checkpoint this machine has ever taken. Nothing asked, and
// the move was announced in the same receipt line as everything else, after it
// had happened. Moving a vault that holds work is the one setup decision worth
// its own answer.
func TestSetupDoesNotMoveAVaultThatHoldsWorkWithoutBeingTold(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")
	old := recordedVaultWithWork(t)

	// --record-temp throughout: every vault here is under the test's temp
	// directory, and that guard is a different bug's (94).
	elsewhere := filepath.Join(t.TempDir(), "second")
	var rec recordOutcome
	out := captureStdout(t, func() {
		var err error
		if _, _, rec, err = chooseVault([]string{"--vault", elsewhere, "--yes", "--record-temp"}, false); err != nil {
			t.Fatal(err)
		}
	})

	if got := vault.Recorded(); got != old {
		t.Errorf("this machine's vault moved to %q with nobody deciding to move it", got)
	}
	if rec != recordSkipMove {
		t.Errorf("record outcome = %v, want recordSkipMove so setup says why nothing was recorded", rec)
	}
	if !strings.Contains(out, "--move-vault") {
		t.Errorf("nothing said how to move it deliberately:\n%s", out)
	}
}

// Said deliberately, it moves — and the path it replaced is kept, so a move
// made in a script is one someone can still read back.
func TestMoveVaultMovesItAndKeepsThePathItReplaced(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")
	old := recordedVaultWithWork(t)

	// --record-temp throughout: every vault here is under the test's temp
	// directory, and that guard is a different bug's (94).
	elsewhere := filepath.Join(t.TempDir(), "second")
	captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", elsewhere, "--yes", "--record-temp", "--move-vault"}, false); err != nil {
			t.Fatal(err)
		}
	})

	if got := vault.Recorded(); got != elsewhere {
		t.Errorf("recorded vault = %q, want %q after --move-vault", got, elsewhere)
	}
	if got := vault.Previous(); got != old {
		t.Errorf("previous vault = %q, want %q", got, old)
	}
}

// A vault with nothing in it yet is not a decision anybody needs to defend:
// the first `logos setup --vault` after a default-location start must still
// just work.
func TestMovingAnEmptyRecordedVaultNeedsNoExtraFlag(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")
	empty := t.TempDir()
	if err := vault.Record(empty); err != nil {
		t.Fatal(err)
	}

	// --record-temp throughout: every vault here is under the test's temp
	// directory, and that guard is a different bug's (94).
	elsewhere := filepath.Join(t.TempDir(), "second")
	captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", elsewhere, "--yes", "--record-temp"}, false); err != nil {
			t.Fatal(err)
		}
	})

	if got := vault.Recorded(); got != elsewhere {
		t.Errorf("recorded vault = %q, want %q — an empty vault moves without ceremony", got, elsewhere)
	}
}
