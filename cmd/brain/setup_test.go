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
	dir, created, _, err := chooseVault([]string{"--vault", scratch}, true)
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
	dir, created, _, err := chooseVault([]string{"--vault", scratch}, false)
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

// A scratch vault named by BRAIN_VAULT is for this process, not for this
// machine. Setup used to record it anyway, and because the recorded pointer is
// ignored only when the directory is *gone* — not when it is merely the wrong
// one — a throwaway vault under an agent's job directory stayed the answer for
// every front end afterwards. The symptom is the worst one this product has:
// resume, sessions and doctor all truthfully report an empty vault while the
// real history sits untouched somewhere else.
func TestSetupDoesNotMakeAScratchVaultNamedByTheEnvironmentThisMachinesVault(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	real := filepath.Join(t.TempDir(), "the-real-one")
	// Recorded() ignores a pointer whose directory is gone, so the vault this
	// test is protecting has to actually be there.
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(real); err != nil {
		t.Fatal(err)
	}

	scratch := filepath.Join(t.TempDir(), "survey-vault")
	t.Setenv("BRAIN_VAULT", scratch)

	dir, _, rec, err := chooseVault(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if dir != scratch {
		t.Errorf("chooseVault returned %q, want the BRAIN_VAULT vault %q to act on", dir, scratch)
	}
	if rec != recordSkipEnv {
		t.Errorf("record outcome = %v for a vault named only by BRAIN_VAULT, want recordSkipEnv", rec)
	}
	if got := vault.Recorded(); got != real {
		t.Errorf("this machine's vault moved to %q; it must still be %q", got, real)
	}
}

// `npx -y @noeton/logos setup` is the install line the README leads with, and
// under npx the running binary is a copy in a cache npm prunes. Writing that
// path into every host config wired the machine to a file that later stops
// existing — setup reports "Working", and the failure arrives weeks later as a
// host that cannot launch its MCP server, with nothing pointing back at the
// install that caused it.
func TestAnNpxInstallWiresHostsToTheCommandRatherThanTheCachedBinary(t *testing.T) {
	npxBin := "/Users/someone/.npm/_npx/2f3ac/node_modules/@noeton/logos/bin/brain"
	srv := serverFor(npxBin, "/Users/someone/brain")

	if strings.Contains(srv.Bin, "_npx") {
		t.Errorf("host config points at %q, a path npm's cache prune deletes", srv.Bin)
	}
	if srv.Bin != "npx" {
		t.Errorf("Bin = %q, want npx so the host resolves a copy on demand", srv.Bin)
	}
	if got := strings.Join(srv.Args, " "); got != "-y @noeton/logos mcp serve" {
		t.Errorf("Args = %q, want the launcher npm/README.md documents", got)
	}
	if srv.Env["BRAIN_VAULT"] != "/Users/someone/brain" {
		t.Errorf("BRAIN_VAULT = %q, the vault must be pinned however brain was installed", srv.Env["BRAIN_VAULT"])
	}

	// An ordinary install is still wired by absolute path: there is no command
	// name that resolves to it, and the file is one the user put there.
	plain := serverFor("/usr/local/bin/brain", "/Users/someone/brain")
	if plain.Bin != "/usr/local/bin/brain" {
		t.Errorf("Bin = %q, want the binary's own path for a standalone install", plain.Bin)
	}
}

// The one command whose entire job is previewing must not describe the opposite
// of what follows. Under BRAIN_VAULT the real run records nothing, and the dry
// run said "would be recorded — the desktop app opens this vault too".
func TestADryRunUnderBrainVaultDoesNotPromiseARecordingThatWillNotHappen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRAIN_VAULT", filepath.Join(t.TempDir(), "scratch"))

	if _, _, rec, err := chooseVault(nil, true); err != nil {
		t.Fatal(err)
	} else if rec != recordSkipEnv {
		t.Errorf("dry run reported %v, want recordSkipEnv — the real run records nothing here", rec)
	}
}
