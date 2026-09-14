package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/vault"
)

// --dry-run is the flag a careful person reaches for first, and it used to be
// the one that did the damage: it created the vault directory and then recorded
// it as this machine's vault. That recording is what the desktop app reads to
// find the vault at all — it is launched from Finder and inherits no
// LOGOS_VAULT — so previewing a setup against a scratch directory silently
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
	dir, created, _, err := chooseVault([]string{"--vault", scratch, "--yes"}, false)
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

// A bare `logos setup` resolves to the recorded vault. With that vault on a
// drive that is not mounted, it made an empty directory at the mount path and
// indexed it — a local folder the drive then mounts over, or a second vault
// the next command reads instead of the real one.
func TestSetupDoesNotCreateARecordedVaultThatIsNotMounted(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")

	ext := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(ext, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(ext); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ext); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := chooseVault(nil, false)
	if err == nil || !strings.Contains(err.Error(), ext) {
		t.Fatalf("setup should refuse and name the recorded vault, got: %v", err)
	}
	if _, err := os.Stat(ext); err == nil {
		t.Error("the unmounted vault's path was created as an empty directory")
	}

	// Choosing somewhere else is still the way out.
	elsewhere := filepath.Join(t.TempDir(), "new")
	if _, _, _, err := chooseVault([]string{"--vault", elsewhere}, false); err != nil {
		t.Fatalf("--vault must still move the pointer: %v", err)
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
	srv, ok := parsed.Servers["logos"]
	if !ok {
		t.Fatalf("no \"logos\" entry in printed config:\n%s", out)
	}
	if srv.Env["LOGOS_VAULT"] != vaultDir {
		t.Errorf("LOGOS_VAULT = %q, want %q", srv.Env["LOGOS_VAULT"], vaultDir)
	}
	// --print-config must never create or record the vault: it is describing a
	// config for a host logos cannot see, not registering one it can.
	if _, err := os.Stat(vaultDir); err == nil {
		t.Errorf("--print-config created %s", vaultDir)
	}
	if _, err := os.Stat(filepath.Join(cfg, "Library", "Application Support", "logos", "vault-path")); err == nil {
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

	if !strings.Contains(out, "[mcp_servers.logos]") {
		t.Errorf("toml output missing [mcp_servers.logos] table:\n%s", out)
	}
	if !strings.Contains(out, vaultDir) {
		t.Errorf("toml output does not mention the vault path %q:\n%s", vaultDir, out)
	}
}

// --config <path> is the write-it-for-me half of the same escape hatch: it
// merges logos's server block into a config file at a location logos has no
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
	if parsed.Servers["logos"].Env["LOGOS_VAULT"] != vaultDir {
		t.Errorf("written config's LOGOS_VAULT = %q, want %q", parsed.Servers["logos"].Env["LOGOS_VAULT"], vaultDir)
	}

	// Same non-registration guarantee as --print-config: describing a config
	// for a client logos cannot see must not create or record a vault either.
	if _, err := os.Stat(vaultDir); err == nil {
		t.Errorf("--config created %s", vaultDir)
	}
}

// A scratch vault named by LOGOS_VAULT is for this process, not for this
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
	t.Setenv("LOGOS_VAULT", scratch)

	dir, _, rec, err := chooseVault(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if dir != scratch {
		t.Errorf("chooseVault returned %q, want the LOGOS_VAULT vault %q to act on", dir, scratch)
	}
	if rec != recordSkipEnv {
		t.Errorf("record outcome = %v for a vault named only by LOGOS_VAULT, want recordSkipEnv", rec)
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
	npxBin := "/Users/someone/.npm/_npx/2f3ac/node_modules/@noeton/logos/bin/logos"
	srv := serverFor(npxBin, "/Users/someone/logos")

	if strings.Contains(srv.Bin, "_npx") {
		t.Errorf("host config points at %q, a path npm's cache prune deletes", srv.Bin)
	}
	if srv.Bin != "npx" {
		t.Errorf("Bin = %q, want npx so the host resolves a copy on demand", srv.Bin)
	}
	if got := strings.Join(srv.Args, " "); got != "-y @noeton/logos mcp serve" {
		t.Errorf("Args = %q, want the launcher npm/README.md documents", got)
	}
	if srv.Env["LOGOS_VAULT"] != "/Users/someone/logos" {
		t.Errorf("LOGOS_VAULT = %q, the vault must be pinned however logos was installed", srv.Env["LOGOS_VAULT"])
	}

	// An ordinary install is still wired by absolute path: there is no command
	// name that resolves to it, and the file is one the user put there.
	plain := serverFor("/usr/local/bin/logos", "/Users/someone/logos")
	if plain.Bin != "/usr/local/bin/logos" {
		t.Errorf("Bin = %q, want the binary's own path for a standalone install", plain.Bin)
	}
}

// Setup resolves its own symlinks, so under Homebrew it found
// <prefix>/Cellar/logos-mcp/<version>/bin/logos and wrote that into every host.
// `brew upgrade` deletes that directory, and each host then fails to launch
// logos with nothing pointing back at the upgrade. The opt link is Homebrew's
// path that follows upgrades.
func TestAHomebrewInstallWiresHostsToThePathThatSurvivesBrewUpgrade(t *testing.T) {
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "logos-mcp", "0.4.3", "bin", "logos")
	opt := filepath.Join(prefix, "opt", "logos-mcp", "bin", "logos")
	if err := os.MkdirAll(filepath.Dir(opt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opt, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv := serverFor(cellar, "/Users/someone/logos")
	if srv.Bin != opt {
		t.Errorf("Bin = %q, want %q, which brew upgrade keeps", srv.Bin, opt)
	}
	if got := strings.Join(srv.Args, " "); got != "mcp serve" {
		t.Errorf("Args = %q, want mcp serve", got)
	}

	// The opt link is a file on this machine, so the integration check launches
	// exactly what the hosts will, with no "resolves on demand" note meant for npx.
	bin, _, note := probeTarget(cellar, srv)
	if bin != opt || note != "" {
		t.Errorf("probe launches %q with note %q, want %q and no note", bin, note, opt)
	}
}

// The one command whose entire job is previewing must not describe the opposite
// of what follows. Under LOGOS_VAULT the real run records nothing, and the dry
// run said "would be recorded — the desktop app opens this vault too".
func TestADryRunUnderLogosVaultDoesNotPromiseARecordingThatWillNotHappen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LOGOS_VAULT", filepath.Join(t.TempDir(), "scratch"))

	if _, _, rec, err := chooseVault(nil, true); err != nil {
		t.Fatal(err)
	} else if rec != recordSkipEnv {
		t.Errorf("dry run reported %v, want recordSkipEnv — the real run records nothing here", rec)
	}
}
