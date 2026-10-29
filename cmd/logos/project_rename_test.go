package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is the CLI surface for the bug that motivated --merge: a checkout
// whose project got renamed after it already had history ends up with a
// checkpoint under sessions/<old>/ and more checkpoints under sessions/<new>/,
// and `logos resume` only ever sees the second directory. `logos project
// rename <old> <new>` alone refuses when <new> already exists, so it cannot
// heal this — --merge has to be wired all the way from the flag to
// rename.Run for the CLI, not just the package, to fix it.
//
// A scratch vault only — never ~/logos — per this repository's safety rule
// for anything that writes to a vault.
func TestProjectRenameMergeCombinesTwoSplitHistories(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_PROJECT", "")

	write := func(rel, body string) {
		p := filepath.Join(vaultDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/brain/20260101-000000-claude.md", "---\ntype: checkpoint\nproject: brain\nagent: claude\n---\n\nold-name era\n")
	write("sessions/logos/20260201-000000-claude.md", "---\ntype: checkpoint\nproject: logos\nagent: claude\n---\n\nnew-name era\n")

	out := captureStdout(t, func() {
		if err := runProjectRename([]string{"logos", "brain", "--merge"}); err != nil {
			t.Fatalf("project rename --merge: %v", err)
		}
	})

	if !strings.Contains(out, "merged") {
		t.Errorf("the run did not announce that it merged:\n%s", out)
	}
	if !strings.Contains(out, "1 checkpoint file(s) moved") {
		t.Errorf("the run did not announce how many checkpoint files moved:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "logos")); !os.IsNotExist(err) {
		t.Error("the old sessions/logos directory still exists after a merge")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "brain", "20260101-000000-claude.md")); err != nil {
		t.Error("the pre-existing brain checkpoint did not survive the merge")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "brain", "20260201-000000-claude.md")); err != nil {
		t.Error("the migrated logos checkpoint did not survive the merge")
	}
}

// --dry-run --merge together must still write nothing, matching the promise
// --dry-run already makes for a plain rename.
func TestProjectRenameMergeDryRunWritesNothing(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_PROJECT", "")

	p := filepath.Join(vaultDir, "sessions", "logos", "20260201-000000-claude.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: checkpoint\nproject: logos\nagent: claude\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vaultDir, "sessions", "brain"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runProjectRename([]string{"logos", "brain", "--merge", "--dry-run"}); err != nil {
			t.Fatalf("dry-run merge: %v", err)
		}
	})
	if !strings.Contains(out, "would merge") {
		t.Errorf("a dry-run merge did not say it was only showing the plan:\n%s", out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("a dry-run merge moved a file")
	}
}

// A merge that drops the old ledgers says so, with the number and the
// command that rebuilds them, rather than leaving a ledger a user opened
// yesterday quietly gone.
func TestProjectRenameMergeSaysItDroppedTheLedgersAndHowToRebuildThem(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_PROJECT", "")
	write := func(rel, body string) {
		p := filepath.Join(vaultDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/brain/20260101-000000-claude.md", "---\ntype: checkpoint\nproject: brain\nagent: claude\n---\n\nold-name era\n")
	write("sessions/logos/20260201-000000-claude.md", "---\ntype: checkpoint\nproject: logos\nagent: claude\n---\n\nnew-name era\n")
	write("sessions/brain/ledger.md", "---\ntype: ledger\nproject: brain\n---\n\n# Ledger: brain\n")
	write("sessions/logos/ledger.md", "---\ntype: ledger\nproject: logos\n---\n\n# Ledger: logos\n")

	out := captureStdout(t, func() {
		if err := runProjectRename([]string{"logos", "brain", "--merge"}); err != nil {
			t.Fatalf("project rename --merge: %v", err)
		}
	})
	if !strings.Contains(out, "dropped 2 ledger(s)") || !strings.Contains(out, "logos ledger brain") {
		t.Errorf("the run did not say it dropped the ledgers and how to rebuild them:\n%s", out)
	}
	if !strings.Contains(out, "1 checkpoint file(s) moved") || strings.Contains(out, "collision") {
		t.Errorf("a ledger was counted as a moved checkpoint or a collision:\n%s", out)
	}
}

// The ledgers go before anything moves, so a rename that fails later has
// already deleted them; saying only the error would leave a ledger the user
// opened yesterday gone without a word.
func TestARenameThatFailsAfterDroppingTheLedgerStillSaysItDroppedIt(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_PROJECT", "")
	write := func(rel, body string) {
		p := filepath.Join(vaultDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/logos/20260201-000000-claude.md", "---\ntype: checkpoint\nproject: logos\nagent: claude\n---\n\nnew-name era\n")
	write("sessions/logos/ledger.md", "---\ntype: ledger\nproject: logos\n---\n\n# Ledger: logos\n")
	// A file where the memories folder belongs fails the memory rewrite, a
	// step that runs after the ledgers are gone.
	write("memories", "not a folder\n")

	var runErr error
	out := captureStdout(t, func() {
		runErr = runProjectRename([]string{"logos", "brain"})
	})
	if runErr == nil {
		t.Fatal("the rename succeeded over a memories file; the test needs a later step to fail")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "logos", "ledger.md")); !os.IsNotExist(err) {
		t.Fatalf("the ledger was not dropped (%v); the test needs it gone before the failure", err)
	}
	if !strings.Contains(out, "dropped 1 ledger(s)") {
		t.Errorf("a rename that failed after deleting the ledger did not say so:\n%s", out)
	}
}

// A folder holding only a ledger is not a typo'd name: the rename deleted
// something, and says what.
func TestRenamingALedgerOnlyFolderSaysItDroppedTheLedger(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_PROJECT", "")
	p := filepath.Join(vaultDir, "sessions", "logos", "ledger.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: ledger\nproject: logos\n---\n\n# Ledger: logos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runProjectRename([]string{"logos", "brain"}); err != nil {
			t.Fatalf("project rename: %v", err)
		}
	})
	if strings.Contains(out, "nothing filed") || !strings.Contains(out, "dropped 1 ledger(s)") {
		t.Errorf("a ledger-only rename read as nothing filed, or did not report the drop:\n%s", out)
	}
}
