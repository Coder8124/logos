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
	cfg := filepath.Join(t.TempDir(), name+".json")
	raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"logos": map[string]any{
		"command": "/opt/logos", "args": []string{"mcp", "serve"},
		"env": map[string]string{"LOGOS_VAULT": pin},
	}}})
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h := fakeHost(name, true, nil)
	h.Config = func() string { return cfg }
	h.Register = func(s setup.Server) (setup.Outcome, error) {
		*got = s.Env["LOGOS_VAULT"]
		return setup.Updated, nil
	}
	return h
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

// --yes is consent to the move, not to wiring every host to a binary Go
// deletes the moment `go run` exits; after that no re-pinned host could start.
func TestMigrateFromAGoRunBuildMovesNothing(t *testing.T) {
	home, cp := oldVaultHome(t)
	var cursor string
	hostsOnMachine(t, pinnedHost(t, "Cursor", filepath.Join(home, "brain"), &cursor))
	old := executable
	executable = func() (string, error) { return filepath.Join(home, "go-build123", "b001", "exe", "logos"), nil }
	t.Cleanup(func() { executable = old })

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err == nil {
		t.Errorf("migrate from a go run build succeeded:\n%s", out)
	}
	if cursor != "" {
		t.Errorf("Cursor was wired to a go run build: %q", cursor)
	}
	if _, err := os.Stat(filepath.Join(home, "brain", cp)); err != nil {
		t.Errorf("the vault was moved before the refusal: %v", err)
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
