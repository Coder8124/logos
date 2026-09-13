package main

import (
	"os"
	"strings"
	"testing"
)

// `logos setup --help` used to run setup: it created ~/logos, recorded it as
// the machine's vault, and at a terminal asked to wire every host with Enter
// meaning yes. Someone who only wanted the flag list got an install.
func TestSetupHelpPrintsTheFlagsAndCreatesNothing(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		dir := setupInFakeHome(t)
		var err error
		out := captureStdout(t, func() { err = setupCmd([]string{flag}) })
		if err != nil {
			t.Fatalf("setup %s: %v", flag, err)
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("setup %s created the vault at %s", flag, dir)
		}
		for _, want := range []string{"--vault", "--host", "--print-config", "--config"} {
			if !strings.Contains(out, want) {
				t.Errorf("setup %s output does not mention %s:\n%s", flag, want, out)
			}
		}
	}
}

// With no vault yet, `logos mcp install --help` answered "vault not found …
// run `logos setup` first" instead of the flags it takes.
func TestMcpInstallHelpPrintsTheFlagsInsteadOfAVaultError(t *testing.T) {
	setupInFakeHome(t)
	var err error
	out := captureStdout(t, func() { err = mcpInstallCmd([]string{"install", "--help"}) })
	if err != nil {
		t.Fatalf("mcp install --help: %v", err)
	}
	if !strings.Contains(out, "logos mcp install [--vault DIR]") {
		t.Errorf("mcp install --help did not print its usage:\n%s", out)
	}
}
