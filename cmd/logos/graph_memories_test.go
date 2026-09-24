package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project in most vaults is a directory of checkpoints and a line in some
// memories, with no note of its own. The graph dropped every edge into a
// project it had no note for, so a vault of forty-five checkpoints drew as a
// line of four and its memories not at all. Everything drawn comes from the
// markdown, so the picture outlives the index (invariant 1).
func TestAProjectsCheckpointsAndMemoriesMeetAtItsHubAndSurviveDeletingTheIndex(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")

	const fact = "the waveguide costs 4.20 dollars per unit"
	if err := memoryCmd([]string{"add", "--project", "kestrel", fact}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	for _, task := range []string{"quote the waveguide", "cut the BOM"} {
		if err := runCheckpoint([]string{"--task", task}); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}

	list := func() string {
		return captureStdout(t, func() {
			if err := runGraph("kestrel", 2, false, true); err != nil {
				t.Fatal(err)
			}
		})
	}
	before := list()
	for _, want := range []string{"◉ projects/kestrel", "project", "memory:", "the waveguide costs", "—about→ projects/kestrel", "—checkpoint_of→ projects/kestrel"} {
		if !strings.Contains(before, want) {
			t.Errorf("missing %q in the graph of a project with no note:\n%s", want, before)
		}
	}

	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}
	if after := list(); after != before {
		t.Errorf("the graph changed when the index was rebuilt from the vault:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
