package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// --dry-run is the flag a careful person reaches for first, and it used to be
// the one that did the damage: it created the vault directory and then recorded
// it as this machine's vault. That recording is what the desktop app reads to
// find the vault at all — it is launched from Finder and inherits no
// BRAIN_VAULT — so previewing a setup against a scratch directory silently
// repointed the app away from the user's real memory, which then reported a
// healthy zero of everything.
func TestADryRunSetupNeitherCreatesTheVaultNorRepointsTheMachine(t *testing.T) {
	// A config directory of our own, so the real pointer is never touched.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	before := vault.Recorded()

	scratch := filepath.Join(t.TempDir(), "try-it")
	dir, created, err := chooseVault([]string{"--vault", scratch}, true)
	if err != nil {
		t.Fatal(err)
	}

	if dir != scratch {
		t.Errorf("chooseVault returned %q, want the path that was asked for", dir)
	}
	if !created {
		t.Error("created = false; a dry run still has to report that the directory is missing")
	}
	if _, err := os.Stat(scratch); err == nil {
		t.Errorf("--dry-run created %s", scratch)
	}
	if now := vault.Recorded(); now != before {
		t.Errorf("--dry-run repointed this machine's vault from %q to %q", before, now)
	}
}

// The other half: a real setup must still do both, or the desktop app never
// finds a vault that is not at the default.
func TestARealSetupCreatesTheVaultAndRecordsIt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	scratch := filepath.Join(t.TempDir(), "for-real")
	dir, created, err := chooseVault([]string{"--vault", scratch}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false for a directory that did not exist")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("setup did not create the vault: %v", err)
	}
	// Private from the first mkdir, not tightened later.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("new vault is mode %v; it holds every note and must not be readable by other accounts", perm)
	}
	if got := vault.Recorded(); got != dir {
		t.Errorf("recorded vault = %q, want %q — the desktop app reads this", got, dir)
	}
}

// "last checkpoint 1 minutes ago" is the kind of line a reviewer notices before
// anything else. This copy of internal/health's formatter dropped the plural
// when it was made.
func TestContinuityDoesNotSayOneMinutes(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{90 * time.Second, "1 minute"},
		{5 * time.Minute, "5 minutes"},
		{90 * time.Minute, "1 hour"},
		{5 * time.Hour, "5 hours"},
		{36 * time.Hour, "36 hours"},
		{50 * time.Hour, "2 days"},
	} {
		if got := roughAge(now.Add(-tc.ago).Unix()); got != tc.want {
			t.Errorf("roughAge(%s ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

// --print-config exists for the MCP client that is not one of setup.Hosts()'s
// curated four: this test is the proof that the flag actually reaches
// setupCmd and prints a real, parseable server block, not just that
// setup.RenderConfig works in isolation.
func TestPrintConfigPrintsAParseableServerBlock(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	vaultDir := filepath.Join(t.TempDir(), "myvault")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--print-config", "--vault", vaultDir}); err != nil {
			t.Fatal(err)
		}
	})

	var parsed struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("--print-config did not print valid JSON: %v\noutput was:\n%s", err, out)
	}
	srv, ok := parsed.Servers["brain"]
	if !ok {
		t.Fatalf("no \"brain\" entry in printed config:\n%s", out)
	}
	if srv.Env["BRAIN_VAULT"] != vaultDir {
		t.Errorf("BRAIN_VAULT = %q, want %q", srv.Env["BRAIN_VAULT"], vaultDir)
	}
	// --print-config must never create or record the vault: it is describing a
	// config for a host brain cannot see, not registering one it can.
	if _, err := os.Stat(vaultDir); err == nil {
		t.Errorf("--print-config created %s", vaultDir)
	}
	if _, err := os.Stat(filepath.Join(cfg, "Library", "Application Support", "brain", "vault-path")); err == nil {
		t.Errorf("--print-config recorded a vault pointer for this machine")
	}
}

// --format toml is the second half of the same escape hatch, for a host (like
// Codex) whose config file is TOML rather than JSON.
func TestPrintConfigFormatTomlNamesTheServerTable(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	vaultDir := filepath.Join(t.TempDir(), "myvault")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--print-config", "--format", "toml", "--vault", vaultDir}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "[mcp_servers.brain]") {
		t.Errorf("toml output missing [mcp_servers.brain] table:\n%s", out)
	}
	if !strings.Contains(out, vaultDir) {
		t.Errorf("toml output does not mention the vault path %q:\n%s", vaultDir, out)
	}
}

// --config <path> is the write-it-for-me half of the same escape hatch: it
// merges brain's server block into a config file at a location brain has no
// built-in convention for, reusing the exact JSON-merge Claude Desktop and
// Cursor already get.
func TestConfigMergesIntoAnArbitraryFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	vaultDir := filepath.Join(t.TempDir(), "myvault")
	target := filepath.Join(t.TempDir(), "some-client", "config.json")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--config", target, "--vault", vaultDir}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, target) {
		t.Errorf("output does not mention the config path it wrote:\n%s", out)
	}

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("--config did not write %s: %v", target, err)
	}
	var parsed struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("written config is not valid JSON: %v\ncontent:\n%s", err, b)
	}
	if parsed.Servers["brain"].Env["BRAIN_VAULT"] != vaultDir {
		t.Errorf("written config's BRAIN_VAULT = %q, want %q", parsed.Servers["brain"].Env["BRAIN_VAULT"], vaultDir)
	}

	// Same non-registration guarantee as --print-config: describing a config
	// for a client brain cannot see must not create or record a vault either.
	if _, err := os.Stat(vaultDir); err == nil {
		t.Errorf("--config created %s", vaultDir)
	}
}
