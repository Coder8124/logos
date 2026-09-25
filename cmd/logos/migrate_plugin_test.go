package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
)

// installPlugin records the Logos plugin as installed for the user in the fake
// home's Claude Code, which is what makes it connect logos on its own.
func installPlugin(t *testing.T, home string) {
	t.Helper()
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"plugins":{"logos@noeton":[{"scope":"user","version":"0.4.9"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #207: with the plugin connecting logos, re-pinning 0.4's brain entry wrote a
// logos entry beside the plugin's server, and the next doctor failed on a
// duplicate registration after a migrate that had looked clean. The plugin
// follows the vault migrate records, so the brain entry has only to go.
func TestMigrateDropsClaudeCodesBrainEntryWhenThePluginAlreadyConnectsIt(t *testing.T) {
	home, _ := oldVaultHome(t)
	installPlugin(t, home)
	var claude string
	h := pinnedHostAs(t, "Claude Code", setup.OldName, filepath.Join(home, "brain"), &claude)
	hostsOnMachine(t, h)

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	servers := hostServers(t, h.Config())
	if len(servers) != 0 {
		t.Errorf("Claude Code's config beside the plugin holds %v, want nothing\n%s", servers, out)
	}
	if !strings.Contains(out, "plugin") {
		t.Errorf("migrate did not say the plugin is what connects Claude Code now:\n%s", out)
	}
}

// #208: the re-pin kept the command, and on the machine it was found on that
// was a pre-rename `brain` build, which reads BRAIN_VAULT and ignored the
// LOGOS_VAULT migrate handed it — "re-pinned" while still reaching the vault
// only through the ~/brain link.
func TestMigrateRepinsAnEntryRunningA04BrainBinaryOntoThisLogos(t *testing.T) {
	home, _ := oldVaultHome(t)
	var cursor string
	h := pinnedHostAs(t, "Cursor", setup.OldName, filepath.Join(home, "brain"), &cursor)
	writeHostServers(t, h.Config(), map[string]any{
		setup.OldName: map[string]any{"command": filepath.Join(home, "go", "bin", "brain"), "args": []string{"mcp", "serve"},
			"env": map[string]string{"BRAIN_VAULT": filepath.Join(home, "brain")}},
	})
	hostsOnMachine(t, h)
	self := filepath.Join(home, "opt", "logos")
	old := executable
	executable = func() (string, error) { return self, nil }
	t.Cleanup(func() { executable = old })

	var err error
	out := captureStdout(t, func() { err = migrateCmd([]string{"--yes"}) })
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	entry, _ := hostServers(t, h.Config())[setup.Name].(map[string]any)
	if entry["command"] != self {
		t.Errorf("Cursor still runs %v, not this logos\n%s", entry["command"], out)
	}
	if !strings.Contains(out, "0.4") || !strings.Contains(out, self) {
		t.Errorf("migrate did not say it swapped the 0.4 binary for %s:\n%s", self, out)
	}
}
