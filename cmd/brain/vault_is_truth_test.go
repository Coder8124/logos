package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The invariant this repository has broken four times before (CLAUDE.md): the
// vault is truth, .brain/index.db is a disposable cache, and every writer's
// promise is "delete the index, run brain index, lose nothing". This test
// attacks that promise across every CLI writer reachable in this workstream's
// scope in one pass, rather than one writer at a time — a regression that
// breaks the promise for a single writer while leaving the others intact would
// not be caught by any single package's own tests.
//
// internal/secretary's open loops are deliberately not exercised here: they are
// stored only in the commitments table with no vault markdown behind them at
// all, so they do not survive this sequence — a real vault-is-truth gap, but in
// a package outside this workstream's scope. Noted in the report, not fixed
// here.
func TestVaultIsTruthAcrossEveryWriter(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off")
	t.Setenv("BRAIN_PROJECT", "truthproj")

	const (
		fact        = "the waveguide costs 4.20 dollars per unit"
		workingNote = "re-quoted the waveguide; no movement under 10k units"
		nextStep    = "quote the single-mic line"
		failedTry   = "switching to a plastic frame did not save enough weight"
	)

	if err := memoryCmd([]string{"add", fact}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	if err := runNote([]string{workingNote}); err != nil {
		t.Fatalf("note: %v", err)
	}
	if err := runCheckpoint([]string{"--next", nextStep, "--failed", failedTry}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// The cache, not the vault: this is the part of the promise under test.
	if err := os.RemoveAll(filepath.Join(vaultDir, ".brain")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatalf("reindex after deleting the cache: %v", err)
	}

	memOut := captureStdout(t, func() {
		if err := memoryCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(memOut, fact) {
		t.Errorf("a memory did not survive deleting .brain and reindexing:\nwant substring %q\ngot:\n%s", fact, memOut)
	}

	resumeOut := captureStdout(t, func() {
		if err := runResume([]string{"truthproj"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{nextStep, failedTry} {
		if !strings.Contains(resumeOut, want) {
			t.Errorf("a checkpoint field did not survive deleting .brain and reindexing:\nwant substring %q\ngot:\n%s", want, resumeOut)
		}
	}
}
