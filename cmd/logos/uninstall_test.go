package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
)

// Uninstall touches host configs only. The vault is the user's memory, and it
// says where that was left so removing it is a decision they make.
func TestUninstallSaysWhatItRemovedAndLeavesTheVault(t *testing.T) {
	dir := setupInFakeHome(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeHosts(t, "Fakey Cursor", "Fakey Codex", "Fakey Zed")
	hosts := detectHosts()
	hosts[0].Remove = func(name string) (bool, error) { return name == setup.Name, nil }
	hosts[1].Remove = func(string) (bool, error) { return false, fmt.Errorf("codex: permission denied") }
	hosts[2].Remove = func(string) (bool, error) { return false, nil }
	detectHosts = func() []setup.Host { return hosts }

	var err error
	out := captureStdout(t, func() { err = mcpUninstallCmd([]string{"uninstall", "--yes"}) })

	for _, want := range []string{"Fakey Cursor", "removed logos", "permission denied", "Fakey Zed", "not registered", dir} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall did not say %q:\n%s", want, out)
		}
	}
	if err == nil {
		t.Error("a host that could not be cleaned was not reported as a failure")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("the vault is gone: %v", statErr)
	}
}

func TestUninstallWithHostOnlyTouchesThatHost(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor", "Fakey Codex")
	hosts := detectHosts()
	var touched []string
	for i := range hosts {
		name := hosts[i].Name
		hosts[i].Remove = func(string) (bool, error) { touched = append(touched, name); return false, nil }
	}
	detectHosts = func() []setup.Host { return hosts }

	captureStdout(t, func() {
		if err := mcpUninstallCmd([]string{"uninstall", "--host", "fakey-codex"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, n := range touched {
		if n != "Fakey Codex" {
			t.Errorf("--host fakey-codex also touched %s", n)
		}
	}
	if err := mcpUninstallCmd([]string{"uninstall", "--host", "nope"}); err == nil || !strings.Contains(err.Error(), "unknown host") {
		t.Errorf("an unknown host was not refused: %v", err)
	}
}

// Setup refuses a flag it does not know before it touches anything; uninstall
// took every flag and read only --host, so `--hosts cursor` was not a typo with
// no effect — it was the absence of the filter, and logos came off every host on
// the machine.
func TestUninstallRefusesAFlagItDoesNotKnowInsteadOfRemovingEveryHost(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor", "Fakey Codex")
	hosts := detectHosts()
	touched := 0
	for i := range hosts {
		hosts[i].Remove = func(string) (bool, error) { touched++; return true, nil }
	}
	detectHosts = func() []setup.Host { return hosts }

	err := mcpUninstallCmd([]string{"uninstall", "--hosts", "fakey-cursor"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag --hosts") {
		t.Errorf("a flag uninstall does not know was accepted: %v", err)
	}
	if touched != 0 {
		t.Errorf("a mistyped flag removed logos from %d host(s)", touched)
	}
}

// Wiring asks first; unwiring asked nothing. A run that takes logos off every
// host names them and waits, and nobody being there is not an answer.
func TestUninstallAsksBeforeRemovingMoreThanOneHost(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor", "Fakey Codex")
	hosts := detectHosts()
	touched := 0
	for i := range hosts {
		hosts[i].Remove = func(string) (bool, error) { touched++; return true, nil }
	}
	detectHosts = func() []setup.Host { return hosts }

	var err error
	out := captureStdout(t, func() { err = mcpUninstallCmd([]string{"uninstall"}) })
	if err != nil {
		t.Fatal(err)
	}
	if touched != 0 {
		t.Errorf("removed logos from %d host(s) without asking", touched)
	}
	for _, want := range []string{"Fakey Cursor", "Fakey Codex", "nothing was removed"} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall did not say %q before stopping:\n%s", want, out)
		}
	}
}

// "none of the hosts logos knows are installed here" is about the machine, and
// it was printed for a run that was about one named host: three hosts were
// installed, and the person disconnecting one read it as "already clean".
func TestUninstallSaysTheSelectedHostIsNotInstalledNotThatNothingIs(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor", "Fakey Codex")
	hosts := detectHosts()
	hosts = append(hosts, setup.Host{
		Name:   "Fakey Zed",
		Detect: func() bool { return false },
		Where:  func() string { return "" },
		Remove: func(string) (bool, error) { return false, nil },
	})
	detectHosts = func() []setup.Host { return hosts }

	out := captureStdout(t, func() {
		if err := mcpUninstallCmd([]string{"uninstall", "--host", "fakey-zed"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Fakey Zed is not installed here") {
		t.Errorf("the message is not about the host that was asked for:\n%s", out)
	}
	if !strings.Contains(out, "Fakey Cursor") {
		t.Errorf("the message does not say what is installed:\n%s", out)
	}
}

// The way in is automated and the way out was homework: uninstall printed
// `/plugin uninstall logos@logos` and left a plugin that kept starting its own
// server, running its hooks and updating itself daily, after a run that
// reported success.
func TestUninstallRemovesTheClaudeCodePluginRatherThanPrintingTheCommand(t *testing.T) {
	setupInFakeHome(t)
	home := os.Getenv("HOME")
	calls := claudeWithPluginCommands(t, home)
	writeClaudeJSON(t, home, "plugins/installed_plugins.json",
		`{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.4.3"}]}}`)
	fakeHosts(t, "Claude Code")

	var err error
	out := captureStdout(t, func() { err = mcpUninstallCmd([]string{"uninstall", "--yes"}) })
	if err != nil {
		t.Fatal(err)
	}
	if ran := readCalls(t, calls); !strings.Contains(ran, "plugin uninstall logos@logos") {
		t.Errorf("uninstall left the plugin installed:\n%s", ran)
	}
	if !strings.Contains(out, "uninstalled") {
		t.Errorf("uninstall did not say the plugin was removed:\n%s", out)
	}
}
