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
// and `brain resume` only ever sees the second directory. `brain project
// rename <old> <new>` alone refuses when <new> already exists, so it cannot
// heal this — --merge has to be wired all the way from the flag to
// rename.Run for the CLI, not just the package, to fix it.
//
// A scratch vault only — never ~/brain — per this repository's safety rule
// for anything that writes to a vault.
func TestProjectRenameMergeCombinesTwoSplitHistories(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_PROJECT", "")

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
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_PROJECT", "")

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
