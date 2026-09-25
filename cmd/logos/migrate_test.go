package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// oldVaultHome is a fake home holding a 0.4 vault at ~/brain, recorded as the
// machine's vault the way carryOldNames records it, with one checkpoint in it.
func oldVaultHome(t *testing.T) (home, checkpoint string) {
	t.Helper()
	home = lastingDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	t.Setenv("LOGOS_HOST", "")
	os.Unsetenv("LOGOS_HOST")
	hostsOnMachine(t)
	checkpoint = filepath.Join("sessions", "brain", "20260901-120000-claude-code.md")
	if err := os.MkdirAll(filepath.Join(home, "brain", "sessions", "brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "brain", checkpoint), []byte("# checkpoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(filepath.Join(home, "brain")); err != nil {
		t.Fatal(err)
	}
	return home, checkpoint
}

// pinnedHost is an installed host whose config pins logos to pin, and which
// records the vault it is re-registered with.
func pinnedHost(t *testing.T, name, pin string, got *string) setup.Host {
	t.Helper()
	return pinnedHostAs(t, name, setup.Name, pin, got)
}

// pinnedHostAs is pinnedHost with the entry registered under entry — brain for
// a host 0.4 setup wired. Register and Remove rewrite the file the way the JSON
// hosts do, so what migrate leaves in it can be read back.
func pinnedHostAs(t *testing.T, name, entry, pin string, got *string) setup.Host {
	t.Helper()
	return pinnedHostWith(t, name, map[string]string{entry: pin}, got)
}

// pinnedHostWith is a host whose config holds one logos entry per name in
// entries, each pinned to the vault it maps to.
func pinnedHostWith(t *testing.T, name string, entries map[string]string, got *string) setup.Host {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), name+".json")
	write := func(servers map[string]any) {
		raw, _ := json.Marshal(map[string]any{"mcpServers": servers})
		if err := os.WriteFile(cfg, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	servers := map[string]any{}
	for entry, pin := range entries {
		servers[entry] = map[string]any{
			"command": "/opt/logos", "args": []string{"mcp", "serve"},
			"env": map[string]string{"LOGOS_VAULT": pin, "LOGOS_EMBED": "off"},
		}
	}
	write(servers)
	h := fakeHost(name, true, nil)
	h.Config = func() string { return cfg }
	h.Register = func(s setup.Server) (setup.Outcome, error) {
		*got = s.Env["LOGOS_VAULT"]
		servers := hostServers(t, cfg)
		servers[setup.Name] = map[string]any{"command": s.Bin, "args": s.Args, "env": s.Env}
		write(servers)
		return setup.Updated, nil
	}
	h.Remove = func(n string) (bool, error) {
		servers := hostServers(t, cfg)
		_, had := servers[n]
		delete(servers, n)
		write(servers)
		return had, nil
	}
	return h
}

// hostServers reads the mcpServers map out of a host config.
func hostServers(t *testing.T, cfg string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.MCPServers == nil {
		c.MCPServers = map[string]any{}
	}
	return c.MCPServers
}

// The point of the command: an early adopter's sessions and checkpoints end up
// under ~/logos, and every later run reads them there.
func TestMigrateMovesTheOldVaultToLogosAndRecordsIt(t *testing.T) {
	home, cp := oldVaultHome(t)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "logos", cp)); err != nil {
		t.Errorf("the checkpoint is not under ~/logos: %v\n%s", err, out)
	}
	if got := vault.Pointer(); got != filepath.Join(home, "logos") {
		t.Errorf("the machine's vault is %q after migrate, want ~/logos", got)
	}
	if !strings.Contains(out, filepath.Join(home, "logos")) {
		t.Errorf("migrate did not say where the vault went:\n%s", out)
	}
}

// A host pinned to ~/brain, a running server, a BRAIN_VAULT in a shell profile:
// everything that still names the old path has to keep reaching the vault.
func TestMigrateLeavesTheOldPathReachingTheMovedVault(t *testing.T) {
	home, cp := oldVaultHome(t)

	captureStdout(t, func() {
		if err := migrateCmd([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	fi, err := os.Lstat(filepath.Join(home, "brain"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/brain is not a link after migrate (%v, %v)", fi, err)
	}
	if _, err := os.Stat(filepath.Join(home, "brain", cp)); err != nil {
		t.Errorf("the checkpoint is not reachable through ~/brain: %v", err)
	}
}

// Each host config pins the vault path in LOGOS_VAULT. Left on ~/brain, a host
// keeps working only for as long as the link does; migrate moves the pin too,
// and only for hosts that were on the old vault.
func TestMigrateRepinsOnlyTheHostsOnTheOldVault(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor, other string
	hostsOnMachine(t,
		pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor),
		pinnedHost(t, "Other", filepath.Join(home, "elsewhere"), &other))

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	if cursor != filepath.Join(home, "logos") {
		t.Errorf("Cursor was re-pinned to %q, want ~/logos\n%s", cursor, out)
	}
	if other != "" {
		t.Errorf("a host on another vault was re-pinned to %q", other)
	}
	if !strings.Contains(out, "Cursor") {
		t.Errorf("migrate did not name the host it re-pinned:\n%s", out)
	}
}

// The vault has moved by then, so this is not "nothing happened" — but a host
// left on the old path is a failure, and exiting 0 would hide it.
func TestMigrateFailsWhenAHostCouldNotBeRepinned(t *testing.T) {
	home, cp := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	h.Register = func(setup.Server) (setup.Outcome, error) {
		return setup.Failed, errors.New("mcp.json is not valid JSON")
	}
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil {
		t.Errorf("migrate succeeded with the only pinned host left on the old path:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "logos", cp)); err != nil {
		t.Errorf("the move itself did not happen: %v", err)
	}
}

// A preview that moved anything would be the worst kind of preview.
func TestMigrateDryRunSaysWhatItWouldDoAndChangesNothing(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	hostsOnMachine(t, pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor))

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--dry-run"}) })
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(home, "logos")); err == nil {
		t.Error("a dry run created ~/logos")
	}
	if got := vault.Pointer(); got != filepath.Join(home, "brain") {
		t.Errorf("a dry run moved the machine's vault to %q", got)
	}
	if cursor != "" {
		t.Error("a dry run re-pinned a host")
	}
	for _, want := range []string{filepath.Join(home, "brain"), filepath.Join(home, "logos"), "Cursor"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not mention %s:\n%s", want, out)
		}
	}
}

// With no terminal and no --yes there is nobody to consent, and moving
// someone's whole vault is not a default.
func TestMigrateWithNobodyToAskMovesNothing(t *testing.T) {
	home, _ := oldVaultHome(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })

	var merr error
	out := captureStdout(t, func() { merr = migrateCmd(nil) })
	if merr == nil {
		t.Errorf("migrate with no answer reported success:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(home, "logos")); err == nil {
		t.Error("migrate moved the vault with nobody to say yes")
	}
}

// Two vaults cannot be merged by a rename, and one of them would be lost.
func TestMigrateRefusesWhenLogosAlreadyExists(t *testing.T) {
	home, cp := oldVaultHome(t)
	if err := os.Mkdir(filepath.Join(home, "logos"), 0o700); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil || !strings.Contains(err.Error(), filepath.Join(home, "logos")) {
		t.Errorf("migrate onto an existing ~/logos did not refuse and name it: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "brain", cp)); err != nil {
		t.Errorf("the old vault was disturbed: %v", err)
	}
}

// Someone who pointed logos somewhere else on purpose is not using ~/brain,
// whatever is sitting there, and moving it would change nothing they use.
func TestMigrateRefusesWhenTheMachineVaultIsSomewhereElse(t *testing.T) {
	home, _ := oldVaultHome(t)
	elsewhere := filepath.Join(home, "notes")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(elsewhere); err != nil {
		t.Fatal(err)
	}

	var err error
	captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil || !strings.Contains(err.Error(), elsewhere) {
		t.Errorf("migrate did not refuse and name the vault in use: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "logos")); err == nil {
		t.Error("migrate moved ~/brain although the machine uses another vault")
	}
}

// Running it twice is what people do when they are not sure the first worked.
func TestMigrateRunTwiceSaysItIsAlreadyDone(t *testing.T) {
	home, _ := oldVaultHome(t)
	captureStdout(t, func() {
		if err := migrateCmd([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
	})

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("a second migrate failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already") || !strings.Contains(out, filepath.Join(home, "logos")) {
		t.Errorf("a second migrate did not say the vault is already at ~/logos:\n%s", out)
	}
}

// Nobody finds a command they are never told about.
func TestTheCLIPointsAtMigrateWhileTheVaultIsStillBrain(t *testing.T) {
	oldVaultHome(t)

	var stderr bytes.Buffer
	migrateHint(&stderr)
	if !strings.Contains(stderr.String(), "logos migrate") {
		t.Errorf("no pointer to migrate while the vault is ~/brain:\n%s", stderr.String())
	}

	captureStdout(t, func() {
		if err := migrateCmd([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	stderr.Reset()
	migrateHint(&stderr)
	if stderr.Len() != 0 {
		t.Errorf("still pointing at migrate after migrating:\n%s", stderr.String())
	}
}

// The Logos plugin connecting Claude Code is why setup leaves Claude Code out,
// but the entry that named ~/brain is still in its config, and the plugin's
// hooks adopt that pin. Skipped here, migrate said "re-pin Claude Code" and
// left it on the old path with exit 0.
func TestMigrateRepinsClaudeCodeWhenThePluginAlreadyConnectsIt(t *testing.T) {
	home, _ := oldVaultHome(t)
	t.Setenv("PATH", t.TempDir())
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"plugins":{"logos@noeton":[{"scope":"user","version":"0.4.9"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var claude string
	hostsOnMachine(t, pinnedHost(t, "Claude Code", filepath.Join(home, "brain"), &claude))

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	if claude != filepath.Join(home, "logos") {
		t.Errorf("Claude Code was left pinned to ~/brain (re-pinned to %q)\n%s", claude, out)
	}
}

// A re-pin changes the vault. Registering the binary that ran migrate instead
// swapped every host onto it — npx, which asks the registry on every launch,
// a copy `brew upgrade` never reaches, an older release, or a `go run` build
// Go deletes on exit — and dropped whatever else the entry set.
func TestMigrateRepinKeepsTheCommandAndEnvironmentTheHostAlreadyRuns(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	hostsOnMachine(t, h)
	old := executable
	executable = func() (string, error) { return filepath.Join(home, "go-build123", "b001", "exe", "logos"), nil }
	t.Cleanup(func() { executable = old })

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	entry, _ := hostServers(t, h.Config())[setup.Name].(map[string]any)
	env, _ := entry["env"].(map[string]any)
	if entry["command"] != "/opt/logos" {
		t.Errorf("Cursor now runs %v, not the /opt/logos it ran before\n%s", entry["command"], out)
	}
	if env["LOGOS_VAULT"] != filepath.Join(home, "logos") || env["LOGOS_EMBED"] != "off" {
		t.Errorf("Cursor's environment after migrate is %v, want the old one with LOGOS_VAULT=~/logos", env)
	}
}

// A host 0.4 setup wired has its entry under brain. Registering logos beside it
// left the host running two servers, one of them still on ~/brain, every tool
// listed twice — and migrate saying it had re-pinned the host.
func TestMigrateReplacesAHostsOldBrainEntryRatherThanAddingASecondServer(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHostAs(t, "Cursor", setup.OldName, filepath.Join(home, "brain"), &cursor)
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	servers := hostServers(t, h.Config())
	if _, ok := servers[setup.OldName]; ok {
		t.Errorf("the 0.4 brain entry is still registered beside logos: %v\n%s", servers, out)
	}
	if entry, _ := servers[setup.Name].(map[string]any); entry["command"] != "/opt/logos" {
		t.Errorf("the logos entry does not run what the brain entry ran: %v", servers)
	}
}

// A host's own CLI replaces an entry outright, and the copy set aside is the
// only way back from a re-pin somebody did not want.
func TestMigrateKeepsABackupOfEachHostConfigItRewrites(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	hostsOnMachine(t, h)

	captureStdout(t, func() {
		if err := migrateCmd([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	raw, err := os.ReadFile(h.Config() + ".logos-backup")
	if err != nil || !strings.Contains(string(raw), filepath.Join(home, "brain")) {
		t.Errorf("no backup of Cursor's config as it was before the re-pin (%v)", err)
	}
}

// A preview changes nothing and keeps no binary, so how it was built is beside
// the point — and `go run ./cmd/logos migrate --dry-run` is how a contributor
// looks first.
func TestMigrateDryRunWorksFromAGoRunBuild(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	hostsOnMachine(t, pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor))
	old := executable
	executable = func() (string, error) { return filepath.Join(home, "go-build123", "b001", "exe", "logos"), nil }
	t.Cleanup(func() { executable = old })

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--dry-run"}) })
	if err != nil {
		t.Errorf("a dry run from a go run build failed: %v\n%s", err, out)
	}
}

// Only an entry under logos or 0.4's brain can be re-pinned: Install writes
// logos and removes brain. A server the user named themselves used to be copied
// into a new logos entry and left where it was, so the host ran two, one still
// on ~/brain — reported as re-pinned.
func TestMigrateLeavesAnEntryUnderAnotherNameAloneAndSaysSo(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHostAs(t, "Cursor", "memory", filepath.Join(home, "brain"), &cursor)
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil || !strings.Contains(err.Error(), "Cursor") {
		t.Errorf("migrate succeeded with Cursor's memory entry still on ~/brain: %v\n%s", err, out)
	}
	if _, ok := hostServers(t, h.Config())[setup.Name]; ok || cursor != "" {
		t.Errorf("migrate added a logos entry beside the user's own: %v", hostServers(t, h.Config()))
	}
	if !strings.Contains(out, "memory") {
		t.Errorf("the report does not name the entry it left:\n%s", out)
	}
}

// A leftover brain entry on ~/brain beside a logos entry on another vault: the
// vault the user chose is the logos one, and re-pinning wrote ~/logos over it,
// on the runs where map order picked brain first.
func TestMigrateNeverRepinsALogosEntryThatUsesAnotherVault(t *testing.T) {
	home, _ := oldVaultHome(t)
	work := filepath.Join(home, "work")
	var cursor string
	h := pinnedHostWith(t, "Cursor", map[string]string{
		setup.Name: work, setup.OldName: filepath.Join(home, "brain"),
	}, &cursor)
	hostsOnMachine(t, h)

	for i := 0; i < 5; i++ { // map order: once is not proof
		captureStdout(t, func() { _ = migrateCmd([]string{"--dry-run"}) })
	}
	out := captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	entry, _ := hostServers(t, h.Config())[setup.Name].(map[string]any)
	env, _ := entry["env"].(map[string]any)
	if env["LOGOS_VAULT"] != work {
		t.Errorf("Cursor's logos entry moved off %s to %v\n%s", work, env["LOGOS_VAULT"], out)
	}
}

// Install adds the session-start hook where there was none, as setup does, and
// a change migrate makes is a change it says it made.
func TestMigrateSaysWhenItAddsAHostsHook(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	h.Hooks = func(setup.Server) (setup.Outcome, error) { return setup.Registered, nil }
	hostsOnMachine(t, h)

	out := captureStdout(t, func() {
		if err := migrateCmd([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "hook") {
		t.Errorf("migrate added Cursor's hook without saying so:\n%s", out)
	}
}

// Claude Code's check for a brain entry runs `claude mcp list`, which can fail
// with nothing to remove. The host was re-pinned; saying it is "still pinned"
// to the old path sends the user to fix something that is not broken.
func TestMigrateDoesNotCallAHostStillPinnedWhenOnlyTheOldEntryCheckFailed(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	h.Remove = func(string) (bool, error) { return false, errors.New("claude mcp list: exit status 1") }
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil {
		t.Errorf("a failed check for the brain entry was not reported as a failure\n%s", out)
	} else if strings.Contains(err.Error(), "still pinned") {
		t.Errorf("migrate calls a re-pinned host still pinned: %v", err)
	}
	if !strings.Contains(out, "re-pinned Cursor") {
		t.Errorf("the re-pin that did happen is not reported:\n%s", out)
	}
}

// writeHostServers replaces the mcpServers map in a host config.
func writeHostServers(t *testing.T, cfg string, servers map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"mcpServers": servers})
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 0.4.0 to 0.4.2 setup pinned BRAIN_VAULT, before the rename. Read only as
// LOGOS_VAULT, those entries had no vault at all: migrate moved ~/brain, left
// them on it, and exited 0 without naming them.
func TestMigrateRepinsAnEntryThatPinsTheVaultAsBrainVault(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHostAs(t, "Cursor", setup.OldName, filepath.Join(home, "brain"), &cursor)
	writeHostServers(t, h.Config(), map[string]any{setup.OldName: map[string]any{
		"command": "/opt/brain", "args": []string{"mcp", "serve"},
		"env": map[string]string{"BRAIN_VAULT": filepath.Join(home, "brain")},
	}})
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	servers := hostServers(t, h.Config())
	entry, _ := servers[setup.Name].(map[string]any)
	env, _ := entry["env"].(map[string]any)
	if env["LOGOS_VAULT"] != filepath.Join(home, "logos") || env["BRAIN_VAULT"] != nil {
		t.Errorf("Cursor's BRAIN_VAULT entry was not re-pinned: %v\n%s", servers, out)
	}
	if _, ok := servers[setup.OldName]; ok {
		t.Errorf("the brain entry is still there: %v", servers)
	}
}

// A logos entry with no pin follows the machine's vault, which migrate records
// as ~/logos. Re-pinning a leftover brain entry beside it wrote brain's binary
// over the one the user runs.
func TestMigrateKeepsTheCommandOfALogosEntryThatFollowsTheMachinesVault(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	writeHostServers(t, h.Config(), map[string]any{
		setup.Name: map[string]any{"command": "/opt/new", "args": []string{"mcp", "serve"}},
		setup.OldName: map[string]any{"command": "/opt/old", "args": []string{"mcp", "serve"},
			"env": map[string]string{"LOGOS_VAULT": filepath.Join(home, "brain")}},
	})
	hostsOnMachine(t, h)

	out := captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	if entry, _ := hostServers(t, h.Config())[setup.Name].(map[string]any); entry["command"] != "/opt/new" {
		t.Errorf("Cursor's logos entry now runs %v, not /opt/new\n%s", entry["command"], out)
	}
	if !strings.Contains(out, "leave") {
		t.Errorf("the brain entry left on ~/brain is not named:\n%s", out)
	}
}

// Re-pinning the logos entry goes through Install, which removes 0.4's brain
// entry — right when that entry was on ~/brain too, and a lost vault choice
// when it named another.
func TestMigrateKeepsABrainEntryThatUsesAnotherVault(t *testing.T) {
	home, _ := oldVaultHome(t)
	work := filepath.Join(home, "work")
	var cursor string
	h := pinnedHostWith(t, "Cursor", map[string]string{
		setup.Name: filepath.Join(home, "brain"), setup.OldName: work,
	}, &cursor)
	hostsOnMachine(t, h)

	out := captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	entry, _ := hostServers(t, h.Config())[setup.OldName].(map[string]any)
	if env, _ := entry["env"].(map[string]any); env["LOGOS_VAULT"] != work {
		t.Errorf("Cursor's brain entry on %s is gone or changed: %v\n%s", work, hostServers(t, h.Config()), out)
	}
}

// A brain entry with no pin follows the machine's vault, which migrate records
// as ~/logos: it is a leftover of the vault being moved, and keeping it ran two
// servers on one vault, with nothing said.
func TestMigrateStillReplacesAnUnpinnedBrainEntry(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	servers := hostServers(t, h.Config())
	servers[setup.OldName] = map[string]any{"command": "/opt/old", "args": []string{"mcp", "serve"}}
	writeHostServers(t, h.Config(), servers)
	hostsOnMachine(t, h)

	out := captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	if _, ok := hostServers(t, h.Config())[setup.OldName]; ok {
		t.Errorf("the unpinned brain entry was kept beside logos\n%s", out)
	}
}

// The plan is what a user checks before saying yes, so an entry migrate will
// remove is in it, not only in the report afterwards.
func TestMigratesPlanSaysWhenItWillRemoveABrainEntry(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	servers := hostServers(t, h.Config())
	servers[setup.OldName] = map[string]any{"command": "/opt/old", "args": []string{"mcp", "serve"}}
	writeHostServers(t, h.Config(), servers)
	hostsOnMachine(t, h)

	out := captureStdout(t, func() { _ = migrateCmd([]string{"--dry-run"}) })
	if !strings.Contains(out, "remove its 0.4 brain entry") {
		t.Errorf("the dry run does not say Cursor's brain entry will be removed:\n%s", out)
	}
}

// LOGOS_VAULT scopes a run to another vault — the scratch-vault habit, or a
// BRAIN_VAULT carried over from a 0.4 profile — and a run scoped elsewhere
// moving the real ~/brain is the one thing it must not do.
func TestMigrateRefusesWhenThisRunIsScopedToAnotherVault(t *testing.T) {
	home, cp := oldVaultHome(t)
	scratch := filepath.Join(home, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_VAULT", scratch)

	var err error
	captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil || !strings.Contains(err.Error(), scratch) {
		t.Errorf("migrate did not refuse and name the vault this run is on: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "brain", cp)); err != nil {
		t.Errorf("~/brain was moved by a run scoped to another vault: %v", err)
	}
}

// A failed re-pin says to fix it, and running migrate again is the fix anyone
// tries first. With ~/brain already a link, the second run said "already at"
// and exited 0 while the host was still pinned to the old path.
func TestMigrateRunAgainRepinsAHostTheFirstRunCouldNot(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	broken := h
	broken.Register = func(setup.Server) (setup.Outcome, error) {
		return setup.Failed, errors.New("mcp.json is locked")
	}
	hostsOnMachine(t, broken)
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })

	hostsOnMachine(t, h)
	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out)
	}
	if cursor != filepath.Join(home, "logos") {
		t.Errorf("the second run left Cursor pinned to %q:\n%s", cursor, out)
	}
}

// The same for the record: a first run that could not write the pointer left
// it naming ~/brain, and a second run has to write it, not call it done.
func TestMigrateRunAgainRecordsTheVaultTheFirstRunCouldNot(t *testing.T) {
	home, _ := oldVaultHome(t)
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	if err := vault.Record(filepath.Join(home, "brain")); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out)
	}
	if p := vault.Pointer(); filepath.Clean(p) != filepath.Join(home, "logos") {
		t.Errorf("the pointer still names %q after a second run:\n%s", p, out)
	}
}

// Without the link, a profile still exporting BRAIN_VAULT=~/brain opens a vault
// that is gone, in every shell after this one; that is not a success.
func TestMigrateFailsWhenItCouldNotLinkTheOldPath(t *testing.T) {
	oldVaultHome(t)
	symlink = func(string, string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { symlink = os.Symlink })

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil {
		t.Errorf("migrate exited 0 with the old path left unlinked:\n%s", out)
	}
}

// A first run that re-pinned logos but could not take out the 0.4 brain entry
// says so; the second run has to take it out. It read the pair as a logos
// entry the user chose beside a brain entry to remove by hand, and failed on
// every run after.
func TestMigrateRunAgainRemovesABrainEntryTheFirstRunCouldNot(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	old := filepath.Join(home, "brain")
	h := pinnedHostWith(t, "Cursor", map[string]string{setup.Name: old, setup.OldName: old}, &cursor)
	stuck := h
	stuck.Remove = func(string) (bool, error) { return false, errors.New("mcp.json is locked") }
	hostsOnMachine(t, stuck)
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })

	hostsOnMachine(t, h)
	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out)
	}
	if _, left := hostServers(t, h.Config())[setup.OldName]; left {
		t.Errorf("the second run left Cursor's brain entry in place:\n%s", out)
	}
}

// Where a link cannot be made — Windows without Developer Mode — a first run
// moves the vault and leaves no ~/brain at all. The second run read that as
// "no vault to move", and a host the first could not re-pin was stuck on a
// path that no longer exists.
func TestMigrateRunAgainFinishesAMoveWhoseLinkFailed(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	broken := h
	broken.Register = func(setup.Server) (setup.Outcome, error) {
		return setup.Failed, errors.New("mcp.json is locked")
	}
	hostsOnMachine(t, broken)
	symlink = func(string, string) error { return errors.New("operation not permitted") }
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })
	symlink = os.Symlink

	hostsOnMachine(t, h)
	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out)
	}
	if cursor != filepath.Join(home, "logos") {
		t.Errorf("the second run left Cursor pinned to %q:\n%s", cursor, out)
	}
	if dest, _ := filepath.EvalSymlinks(filepath.Join(home, "brain")); dest != filepath.Join(home, "logos") {
		t.Errorf("the second run did not make the link: ~/brain resolves to %q", dest)
	}
}

// opencode's own switch for a server. Re-registering writes enabled: true, so
// re-pinning a disabled entry would turn it back on; it is left as it is, and
// said so, and a run that leaves only that is not a failure.
func TestMigrateLeavesADisabledOpencodeEntryOffAndSaysSo(t *testing.T) {
	home, _ := oldVaultHome(t)
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "opencode.json")
	body := `{"mcp": {"logos": {"type": "local", "command": ["/opt/logos", "mcp", "serve"], "enabled": false, "environment": {"LOGOS_VAULT": "` + filepath.Join(home, "brain") + `"}}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, h := range setup.Hosts() {
		if h.Name == "opencode" {
			hostsOnMachine(t, h)
		}
	}

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Errorf("migrate failed over a disabled entry: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(cfg)
	if !strings.Contains(string(raw), `"enabled": false`) {
		t.Errorf("opencode's disabled logos entry was changed:\n%s", raw)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("migrate did not say it left the disabled entry:\n%s", out)
	}
}

// opencodeOnMachine is the real opencode host, with its config holding body.
func opencodeOnMachine(t *testing.T, home, body string) string {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, h := range setup.Hosts() {
		if h.Name == "opencode" {
			hostsOnMachine(t, h)
		}
	}
	return cfg
}

// A fresh 0.5 install records ~/logos and never had ~/brain; that is not a
// move whose link failed, and migrate has no business creating the old path.
func TestMigrateOnAMachineThatNeverHadBrainLinksNothing(t *testing.T) {
	home, _ := oldVaultHome(t)
	if err := os.RemoveAll(filepath.Join(home, "brain")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(filepath.Join(home, "logos")); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Errorf("migrate failed on a machine with nothing to move: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(home, "brain")); !os.IsNotExist(err) {
		t.Errorf("migrate created %s on a machine that never had it:\n%s", filepath.Join(home, "brain"), out)
	}
}

// Windows without Developer Mode refuses every link. Once the hosts are
// re-pinned nothing names ~/brain, and the link no run can make stops being
// a failure — otherwise migrate never succeeds again on that machine.
func TestMigrateSucceedsOnceEveryHostIsRepinnedWhenNoLinkCanBeMade(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor)
	broken := h
	broken.Register = func(setup.Server) (setup.Outcome, error) {
		return setup.Failed, errors.New("mcp.json is locked")
	}
	symlink = func(string, string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { symlink = os.Symlink })
	hostsOnMachine(t, broken)
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })

	hostsOnMachine(t, h)
	for run := 2; run <= 3; run++ {
		var err error
		out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
		if err != nil {
			t.Errorf("run %d failed with every host re-pinned: %v\n%s", run, err, out)
		}
	}
	if cursor != filepath.Join(home, "logos") {
		t.Errorf("Cursor was left pinned to %q", cursor)
	}
}

// Leaving a disabled logos entry off is fine; leaving the enabled 0.4 brain
// entry beside it running on the old path is not, and exiting 0 would hide it.
func TestMigrateReportsAnEnabledBrainEntryLeftBesideADisabledLogosEntry(t *testing.T) {
	home, _ := oldVaultHome(t)
	old := filepath.Join(home, "brain")
	opencodeOnMachine(t, home, `{"mcp": {`+
		`"logos": {"type": "local", "command": ["/opt/logos", "mcp", "serve"], "enabled": false, "environment": {"LOGOS_VAULT": "`+old+`"}},`+
		`"brain": {"type": "local", "command": ["/opt/brain", "mcp", "serve"], "environment": {"BRAIN_VAULT": "`+old+`"}}}}`)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil {
		t.Errorf("migrate exited 0 with opencode's brain entry still on %s:\n%s", old, out)
	}
}

// A disabled entry is left on purpose; it is not work left over, so a move
// that is otherwise finished says so rather than asking to finish it again.
func TestMigrateSaysAlreadyDoneWhenOnlyADisabledEntryIsLeft(t *testing.T) {
	home, _ := oldVaultHome(t)
	opencodeOnMachine(t, home, `{"mcp": {"logos": {"type": "local", "command": ["/opt/logos", "mcp", "serve"], "enabled": false, "environment": {"LOGOS_VAULT": "`+filepath.Join(home, "brain")+`"}}}}`)
	captureStdout(t, func() { _ = migrateCmd([]string{"--yes"}) })

	var err error
	out := captureStdout(t, func() { err = migrateCmd(nil) })
	if err != nil || !strings.Contains(out, "already") {
		t.Errorf("a finished move with only a disabled entry left did not say it is done: %v\n%s", err, out)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("it stopped saying it left the disabled entry:\n%s", out)
	}
}
