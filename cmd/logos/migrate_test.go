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
