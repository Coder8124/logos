package main

import (
	"os"
	"strings"
	"testing"
)

// stdinAt feeds setup's prompts from text, EOF after it.
func stdinAt(t *testing.T, text string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// `brew install … && logos setup` in a provisioning script has no terminal, so
// the wire prompt reads nothing and setup skipped every host — and exited 0.
// The script took a run that wired nothing as a success, and the user found
// out when their AI tool had no Logos. Nobody being there to ask is not a no.
func TestSetupWithNoOneToAskWiresNothingAndFails(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")
	stdinAt(t, "")

	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", dir}) })
	if err == nil {
		t.Fatalf("setup with no terminal wired nothing and succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing was wired") || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error does not say nothing was wired and how to wire unattended: %v", err)
	}
	if strings.Contains(out, "registered") {
		t.Errorf("setup wired a host without an answer:\n%s", out)
	}
}

// The other half: someone who answers n meant it, and that is not a failure.
// (The first n declines recording the temporary vault.)
func TestSetupAnsweredNoWiresNothingAndSucceeds(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")
	stdinAt(t, "n\nn\n")

	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", dir}) })
	if err != nil {
		t.Fatalf("setup answered n failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "registered") {
		t.Errorf("setup wired a host after being told no:\n%s", out)
	}
}
