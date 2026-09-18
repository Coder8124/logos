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
	p := filepath.Join(dir, session.CheckpointDir, "kestrel")
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	// A real checkpoint, not an empty directory: what makes the move expensive
	// is the work in the vault, and that is what this helper has to stand for.
	if err := os.WriteFile(filepath.Join(p, "20260101-100000-cli.md"), []byte("# checkpoint\n"), 0o600); err != nil {
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

// "it holds work" is not enough to answer with. The question setup asks here is
// whether to repoint every front end on the machine away from a vault, and the
// only thing that makes it answerable is how much is in the one being left —
// #90's direction asks for the count by name. A user who cannot see it either
// declines a move they wanted or accepts one that strands months of sessions.
func TestMovingALoadedVaultSaysHowMuchThatVaultHolds(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")

	old := t.TempDir()
	for _, p := range []string{"kestrel", "app"} {
		dir := filepath.Join(old, session.CheckpointDir, p)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"20260101-100000-cli.md", "20260102-100000-cli.md"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte("# checkpoint\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := vault.Record(old); err != nil {
		t.Fatal(err)
	}

	// --record-temp throughout: every vault here is under the test's temp
	// directory, and that guard is a different bug's (94).
	elsewhere := filepath.Join(t.TempDir(), "second")
	out := captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", elsewhere, "--yes", "--record-temp"}, false); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "4 checkpoints across 2 projects") {
		t.Errorf("the vault being left was not described, so the move cannot be judged:\n%s", out)
	}
}

// A session directory holds the project's working notes (uncommitted.md) as
// well as its checkpoints. Counting every .md in it told someone with no
// checkpoints at all that they were about to leave "2 checkpoints" behind.
// Counting them correctly is only half of it: a vault holding no checkpoints
// has nothing to strand, so it moves like any other empty one rather than
// asking a question whose own sentence says there is nothing to lose.
func TestAVaultHoldingOnlyWorkingNotesMovesWithoutCeremony(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")

	old := t.TempDir()
	for _, p := range []string{"kestrel", "app"} {
		dir := filepath.Join(old, session.CheckpointDir, p)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, session.NotesFile), []byte("# notes\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := vault.Record(old); err != nil {
		t.Fatal(err)
	}

	// --record-temp throughout: every vault here is under the test's temp
	// directory, and that guard is a different bug's (94).
	elsewhere := filepath.Join(t.TempDir(), "second")
	out := captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", elsewhere, "--yes", "--record-temp"}, false); err != nil {
			t.Fatal(err)
		}
	})

	if got := vault.Recorded(); got != elsewhere {
		t.Errorf("recorded vault = %q, want %q — a vault with no checkpoints moves without ceremony:\n%s", got, elsewhere, out)
	}
	if strings.Contains(out, "0 checkpoints") {
		t.Errorf("setup asked about leaving behind nothing at all:\n%s", out)
	}
}
