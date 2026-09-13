package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/setup"
)

// hostsOnMachine stubs the host seams with hosts that are installed or not,
// and whose registration succeeds or fails, as each test needs.
func hostsOnMachine(t *testing.T, hosts ...setup.Host) {
	t.Helper()
	old, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host { return hosts }
	integrationChecks = func(string, []string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = old, oldCheck })
}

func fakeHost(name string, installed bool, registerErr error) setup.Host {
	return setup.Host{
		Name:   name,
		Detect: func() bool { return installed },
		Where:  func() string { return "/nowhere/" + name + ".json" },
		Register: func(setup.Server) (setup.Outcome, error) {
			if registerErr != nil {
				return setup.Failed, registerErr
			}
			return setup.Registered, nil
		},
	}
}

// `setup --host codex` with Codex missing and Cursor installed said "No MCP
// hosts found. Install … Cursor" and exited 0: it told someone with Cursor to
// install Cursor, and a script took the run as a success.
func TestNamingAHostThatIsNotInstalledSaysSoAndFails(t *testing.T) {
	dir := setupInFakeHome(t)
	hostsOnMachine(t, fakeHost("Cursor", true, nil), fakeHost("Codex", false, nil))

	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", dir, "--host", "codex", "--yes"}) })
	if err == nil {
		t.Fatalf("setup --host codex with Codex missing succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "Codex is not installed") || !strings.Contains(err.Error(), "Cursor") {
		t.Errorf("error does not say Codex is missing and Cursor is here: %v", err)
	}
	if strings.Contains(out, "No MCP hosts found") {
		t.Errorf("setup still claims no hosts were found:\n%s", out)
	}
}

// With Cursor's config unreadable, setup printed Cursor's error and then "No
// MCP hosts found. Install … Cursor", contradicting the line above it, and
// exited 0 with nothing wired.
func TestAHostThatFailsToWireIsNotReportedAsMissing(t *testing.T) {
	dir := setupInFakeHome(t)
	hostsOnMachine(t, fakeHost("Cursor", true, errors.New("mcp.json is not valid JSON, so it was left alone")))

	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", dir, "--yes"}) })
	if err == nil {
		t.Fatalf("setup succeeded with every host failing:\n%s", out)
	}
	if !strings.Contains(err.Error(), "1 host found, none wired") {
		t.Errorf("error does not count the host that failed: %v", err)
	}
	if strings.Contains(out, "No MCP hosts found") {
		t.Errorf("setup still claims no hosts were found:\n%s", out)
	}
}
