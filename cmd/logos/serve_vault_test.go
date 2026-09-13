package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultmod "github.com/Coder8124/logos/internal/vault"
)

// `/plugin install logos@logos` is the first route the README offers, and it
// wires the MCP server with no LOGOS_VAULT and no setup step. On a laptop that
// had never run `logos setup` the server started, found no ~/logos and exited —
// which Claude Code reports as a failed connection with no cause. The first
// thing a new user saw the product do was fail to start.
func TestTheServerMakesTheDefaultVaultRatherThanRefusingToStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LOGOS_VAULT", "")

	dir, err := serveVault()
	if err != nil {
		t.Fatalf("the server refused to start on a machine that has never run setup: %v", err)
	}
	if dir != filepath.Join(home, "logos") {
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

// The other half of the same rule. A LOGOS_VAULT that points nowhere is a typo,
// and building a fresh empty vault over it is the silent half-vault that
// requireVault exists to prevent: the user's real memory sits one directory
// away while every tool reports a healthy zero of everything.
func TestAVaultSomeoneNamedIsNeverCreatedBehindTheirBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	typo := filepath.Join(home, "barin")
	t.Setenv("LOGOS_VAULT", typo)

	_, err := serveVault()
	if err == nil {
		t.Fatal("a mistyped LOGOS_VAULT was created instead of reported")
	}
	if !strings.Contains(err.Error(), typo) {
		t.Errorf("the error does not name the path that was tried: %v", err)
	}
	if _, err := os.Stat(typo); err == nil {
		t.Error("the mistyped directory was created anyway")
	}
}

// An external drive that is not mounted looks exactly like a deleted vault. The
// server used to treat it as "nobody chose a vault", create ~/logos, and accept
// checkpoints into it — a split the user only finds when `resume` comes back
// empty after the drive returns. It refuses, and names the recorded path.
func TestTheServerRefusesWhenTheRecordedVaultIsNotMounted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LOGOS_VAULT", "")
	ext := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(ext, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vaultmod.Record(ext); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ext); err != nil {
		t.Fatal(err)
	}

	_, err := serveVault()
	if err == nil {
		t.Fatal("the server started with the recorded vault missing")
	}
	if !strings.Contains(err.Error(), ext) {
		t.Errorf("the error does not name the recorded vault: %v", err)
	}
	if strings.Contains(err.Error(), "run `logos setup` to create one") {
		t.Errorf("the error tells the user to create a new vault instead of reconnecting theirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "logos")); err == nil {
		t.Error("~/logos was created while the real vault was unmounted")
	}
}
