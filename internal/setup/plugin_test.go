package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// `claude` is a wrapper around node, so killing the process logos started left
// the work it had started running: a network call for a step already reported
// as failed, and a child still holding the pipes this package reads.
func TestAPluginStepThatTimesOutKillsWhatClaudeStarted(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	marker := filepath.Join(t.TempDir(), "child-outlived-the-step")
	// The timings are generous on purpose: what is being tested is whether the
	// child outlives the kill, and a stub killed before it got as far as
	// starting one would pass either way.
	stub := "#!/bin/sh\n(sleep 1.5; : > " + marker + ") &\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := pluginStepTimeout
	pluginStepTimeout = 700 * time.Millisecond
	t.Cleanup(func() { pluginStepTimeout = prev })

	err := RunPluginSteps(UpdatePluginSteps())
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("RunPluginSteps = %v; want the timeout back", err)
	}
	time.Sleep(1600 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the child claude started outlived the step that timed out")
	}
}
