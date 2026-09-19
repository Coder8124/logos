package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

func writeCheckpoint(t *testing.T, vault, scope, name string) {
	t.Helper()
	dir := filepath.Join(vault, session.CheckpointDir, filepath.FromSlash(scope))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("# checkpoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A worktree's checkpoints live one level down, in sessions/<project>/<worktree>/,
// because safeScope keeps "kestrel/feature-x" as two levels and session.Projects
// returns only the top one. Counting just the files directly under the project
// found nothing, so `logos setup --vault elsewhere` repointed the machine away
// from a vault full of work without asking — the one prompt standing between a
// user and a vault they can no longer find.
func TestAVaultWhoseCheckpointsAreAllInWorktreesStillCountsAsHoldingWork(t *testing.T) {
	vault := t.TempDir()
	writeCheckpoint(t, vault, "kestrel/feature-x", "20260918-120000-claude.md")
	writeCheckpoint(t, vault, "kestrel/feature-y", "20260918-130000-claude.md")

	holding, checkpoints := vaultHolding(vault, []string{"kestrel"})

	if checkpoints != 2 {
		t.Errorf("a vault holding two worktree checkpoints counted %d", checkpoints)
	}
	if !strings.Contains(holding, "2 checkpoints") {
		t.Errorf("the warning does not name what would be left behind: %q", holding)
	}
}

// Checkpoints directly under the project are the ordinary case and must keep
// counting exactly once — a recursive walk that counted both levels would
// double a vault that has some of each.
func TestCheckpointsAreCountedOnceWhetherTheyAreInAWorktreeOrNot(t *testing.T) {
	vault := t.TempDir()
	writeCheckpoint(t, vault, "kestrel", "20260918-120000-claude.md")
	writeCheckpoint(t, vault, "kestrel/feature-x", "20260918-130000-claude.md")
	writeCheckpoint(t, vault, "shop", "20260918-140000-cursor.md")

	holding, checkpoints := vaultHolding(vault, []string{"kestrel", "shop"})

	if checkpoints != 3 {
		t.Errorf("want 3 checkpoints across the two projects, got %d (%q)", checkpoints, holding)
	}
	if !strings.Contains(holding, "2 projects") {
		t.Errorf("want both projects named as held: %q", holding)
	}
}

// The working-notes file sits in the same directory and is not a checkpoint.
// Calling it one overstates what the vault holds, in the sentence a user is
// deciding on.
func TestAProjectWithOnlyWorkingNotesIsNotCountedAsHoldingCheckpoints(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, session.CheckpointDir, "kestrel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.NotesFile), []byte("# uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, checkpoints := vaultHolding(vault, []string{"kestrel"}); checkpoints != 0 {
		t.Errorf("working notes were counted as %d checkpoint(s)", checkpoints)
	}
}
