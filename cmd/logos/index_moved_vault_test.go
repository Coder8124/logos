package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Moving a vault re-points every note's stored path the next time anything
// syncs. The number has to be said (invariant 3): 306 rows rewritten under an
// "=306" is work the person would otherwise never see.
func TestIndexSaysHowManyNotesItRePointedAfterTheVaultMoved(t *testing.T) {
	parent := t.TempDir()
	old := filepath.Join(parent, "old")
	for _, n := range []string{"a", "b"} {
		p := filepath.Join(old, "notes", n+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: note\ntitle: "+n+"\n---\n\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_VAULT", old)
	captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatalf("index: %v", err)
		}
	})

	moved := filepath.Join(parent, "deeper", "new")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_VAULT", moved)

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatalf("index after the move: %v", err)
		}
	})
	if !strings.Contains(out, "the vault moved: re-pointed 2 notes at "+moved) {
		t.Errorf("index did not say it re-pointed the 2 notes:\n%s", out)
	}
	again := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(again, "the vault moved") {
		t.Errorf("a second index claimed the vault moved again:\n%s", again)
	}
}
