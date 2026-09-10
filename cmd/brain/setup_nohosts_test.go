package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Until --no-hosts existed, the only way to create and index a vault was to
// also repoint every AI tool on the machine at it. That is a large thing to
// accept in order to evaluate one integration, and it is the wrong thing
// entirely for a second vault on a machine that already has one wired.
func TestNoHostsSetsUpTheVaultAndWiresNothing(t *testing.T) {
	// A fake home, because setup records the chosen vault where the desktop app
	// reads it. A test that skipped this would repoint the developer's own
	// machine at a temporary directory and leave it there.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	dir := filepath.Join(t.TempDir(), "vault")
	t.Setenv("BRAIN_VAULT", dir)

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--no-hosts", "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "nothing was wired") {
		t.Errorf("setup did not say it wired nothing:\n%s", out)
	}
	// Invariant 3: it says what it did *and* what to run next.
	if !strings.Contains(out, "brain mcp install") {
		t.Errorf("setup did not say how to wire the hosts later:\n%s", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the vault was not created: %v", err)
	}
}
