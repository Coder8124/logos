package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/health"
	"github.com/Coder8124/brain/internal/setup"
)

// fakeHosts replaces host detection and the integration self-test for the
// duration of one test. Nothing here may touch a real config or run a real
// host CLI: an earlier version of this test reached setup.Hosts() and actually
// ran `claude mcp add --scope user` and `codex mcp add` on the developer's
// machine. Only a faked HOME kept it inside a temp directory.
func fakeHosts(t *testing.T, registered ...string) {
	t.Helper()
	var hosts []setup.Host
	for _, name := range registered {
		hosts = append(hosts, setup.Host{
			Name:     name,
			Detect:   func() bool { return true },
			Where:    func() string { return "/nowhere/config.json" },
			Register: func(setup.Server) (setup.Outcome, error) { return setup.Registered, nil },
		})
	}
	old, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host { return hosts }
	integrationChecks = func(string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = old, oldCheck })
}

func setupInFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(t.TempDir(), "vault")
	t.Setenv("BRAIN_VAULT", dir)
	return dir
}

// The roster of hosts is what a person says yes or no to. With --yes there is
// no prompt, so printing it and then printing the same rows again with their
// outcomes made setup — the first screen a new user sees — look like it had
// rendered its output twice.
func TestTheHostListIsNotPrintedTwiceWhenThereIsNothingToConfirm(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if n := strings.Count(out, "Fakey Desktop"); n != 1 {
		t.Errorf("host listed %d times, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "registered") {
		t.Errorf("the one line kept must be the outcome, not the plan:\n%s", out)
	}
}

// --dry-run is nothing but the roster, so there it stays.
func TestADryRunStillShowsWhichHostsItWouldWire(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--dry-run"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "Fakey Desktop") {
		t.Errorf("dry run did not name the host it would wire:\n%s", out)
	}
	if !strings.Contains(out, "nothing was written") {
		t.Errorf("dry run did not say it wrote nothing:\n%s", out)
	}
}
