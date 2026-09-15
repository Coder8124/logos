package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// writeClaudeFile writes one of Claude Code's own records under the fake HOME.
func writeClaudeFile(t *testing.T, home, rel, body string) {
	t.Helper()
	path := filepath.Join(home, ".claude", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Setup took any logos@ record as "Claude Code is already connected" and
// skipped it, so turning the plugin off in /plugin left Claude Code with no
// Logos at all, and setup said it was connected.
func TestADisabledLogosPluginDoesNotConnectClaudeCode(t *testing.T) {
	home := fakeHome(t)
	writeClaudeFile(t, home, "plugins/installed_plugins.json", `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.3"}]}}`)
	writeClaudeFile(t, home, "settings.json", `{"enabledPlugins":{"logos@logos":false}}`)

	r := LogosPluginRecord()
	if _, connects := LogosPlugin(); connects || r.Connects {
		t.Error("a plugin disabled in Claude Code's settings was taken as connecting it")
	}
	if !r.Installed || r.Why == "" {
		t.Errorf("the record must still say a plugin is installed, and why it does not count: %+v", r)
	}
}

// A plugin installed for one project connects Claude Code in that project
// only; every other project was left with nothing.
func TestALogosPluginInstalledForOneProjectDoesNotConnectClaudeCode(t *testing.T) {
	home := fakeHome(t)
	writeClaudeFile(t, home, "plugins/installed_plugins.json", `{"version":2,"plugins":{"logos@logos":[{"scope":"local","projectPath":"/Users/x/someproject","version":"0.4.3"}]}}`)

	r := LogosPluginRecord()
	if r.Connects {
		t.Error("a plugin installed for one project was taken as connecting Claude Code everywhere")
	}
	if r.Why == "" {
		t.Errorf("the record does not say why the plugin does not count: %+v", r)
	}
}

func TestAnEnabledUserScopeLogosPluginConnectsClaudeCode(t *testing.T) {
	home := fakeHome(t)
	writeClaudeFile(t, home, "plugins/installed_plugins.json", `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.3"}]}}`)
	writeClaudeFile(t, home, "settings.json", `{"enabledPlugins":{"logos@logos":true}}`)

	if v, connects := LogosPlugin(); !connects || v != "0.4.3" {
		t.Errorf("LogosPlugin() = %q, %v; want 0.4.3, true", v, connects)
	}
}

// Plugins are keyed by marketplace and map order is random, so a disabled copy
// from one marketplace sometimes hid an enabled one from another.
func TestAnEnabledLogosPluginIsFoundBesideADisabledOneFromAnotherMarketplace(t *testing.T) {
	home := fakeHome(t)
	writeClaudeFile(t, home, "plugins/installed_plugins.json", `{"version":2,"plugins":{"logos@old":[{"scope":"user","version":"0.1.2"}],"logos@logos":[{"scope":"user","version":"0.4.3"}]}}`)
	writeClaudeFile(t, home, "settings.json", `{"enabledPlugins":{"logos@old":false,"logos@logos":true}}`)

	for i := 0; i < 20; i++ {
		if r := LogosPluginRecord(); !r.Connects || r.Version != "0.4.3" {
			t.Fatalf("the enabled plugin was missed: %+v", r)
		}
	}
}
