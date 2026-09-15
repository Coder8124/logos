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
	out := captureStdout(t, func() { err = mcpUninstallCmd([]string{"uninstall"}) })

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
