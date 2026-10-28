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
