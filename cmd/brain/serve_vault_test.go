package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `/plugin install logos@logos` is the first route the README offers, and it
// wires the MCP server with no BRAIN_VAULT and no setup step. On a laptop that
// had never run `brain setup` the server started, found no ~/brain and exited —
// which Claude Code reports as a failed connection with no cause. The first
// thing a new user saw the product do was fail to start.
func TestTheServerMakesTheDefaultVaultRatherThanRefusingToStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("BRAIN_VAULT", "")

	dir, err := serveVault()
	if err != nil {
		t.Fatalf("the server refused to start on a machine that has never run setup: %v", err)
	}
	if dir != filepath.Join(home, "brain") {
		t.Errorf("served %s, not the default vault", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("the vault was not created: %v", err)
	}
	// Private from the first mkdir, exactly as setup makes it.
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("vault created as %v — readable by others", fi.Mode().Perm())
	}
}

// The other half of the same rule. A BRAIN_VAULT that points nowhere is a typo,
// and building a fresh empty vault over it is the silent half-vault that
// requireVault exists to prevent: the user's real memory sits one directory
// away while every tool reports a healthy zero of everything.
func TestAVaultSomeoneNamedIsNeverCreatedBehindTheirBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	typo := filepath.Join(home, "barin")
	t.Setenv("BRAIN_VAULT", typo)

	_, err := serveVault()
	if err == nil {
		t.Fatal("a mistyped BRAIN_VAULT was created instead of reported")
	}
	if !strings.Contains(err.Error(), typo) {
		t.Errorf("the error does not name the path that was tried: %v", err)
	}
	if _, err := os.Stat(typo); err == nil {
		t.Error("the mistyped directory was created anyway")
	}
}
