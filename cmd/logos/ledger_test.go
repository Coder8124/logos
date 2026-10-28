package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command is how the ledger is made, so it has to say what it made: the
// counts and the file, on stdout, where a person running it looks.
func TestLogosLedgerWritesTheFileAndPrintsWhatIsInIt(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	dir := filepath.Join(v, "sessions", "shop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cp := "---\ntype: checkpoint\nproject: shop\nagent: claude-code\ncommit: abc1234\n" +
		"checkpointed: 2026-10-27T21:22:24-07:00\n---\n\n## Task\n\nCheckout\n\n" +
		"## Decisions\n\n- Use signed cookies, because revocation\n\n" +
		"## Didn't work\n\n- Redis session store: unreachable from the edge\n"
	if err := os.WriteFile(filepath.Join(dir, "20261027-212224-claude-code.md"), []byte(cp), 0o600); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() { err = runLedger([]string{"shop"}) })
	if err != nil {
		t.Fatal(err)
	}
	want := "ledger: 1 decision in force, 0 superseded, 1 ruled out"
	if !strings.Contains(out, want) || !strings.Contains(out, "sessions/shop/ledger.md") {
		t.Errorf("the command does not announce what it wrote, want %q in:\n%s", want, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Use signed cookies") || !strings.Contains(string(raw), "Redis session store") {
		t.Errorf("the ledger does not carry the checkpoint's decision and dead end:\n%s", raw)
	}
}

// The vault files "Shop" and "shop" as one project, so a ledger asked for under
// either spelling, from that project's own folder, is that project's: labelled
// with the name its checkpoints are filed under, and measured against the
// checkout the user is standing in rather than treated as another repository's.
func TestALedgerAskedForUnderAnotherLetterCaseIsTheProjectHere(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	t.Setenv("LOGOS_PROJECT", "")
	dir := filepath.Join(v, "sessions", "shop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cp := "---\ntype: checkpoint\nproject: shop\nagent: claude-code\n" +
		"checkpointed: 2026-10-27T21:22:24-07:00\n---\n\n## Task\n\nCheckout\n\n" +
		"## Decisions\n\n- Use signed cookies, because revocation\n"
	if err := os.WriteFile(filepath.Join(dir, "20261027-212224-claude-code.md"), []byte(cp), 0o600); err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(here, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)

	for _, typed := range []string{"Shop", "SHOP"} {
		if repoHere(typed) == "" {
			t.Errorf("standing in shop, %q was taken for another repository's project", typed)
		}
	}
	var err error
	out := captureStdout(t, func() { err = runLedger([]string{"Shop"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sessions/shop/ledger.md") {
		t.Errorf("the ledger was not filed under the project's own folder:\n%s", out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# Ledger: shop\n") || strings.Contains(string(raw), "Shop") {
		t.Errorf("the ledger is labelled with the typed spelling, not the project's:\n%s", raw)
	}
}
