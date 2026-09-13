package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/health"
	"github.com/Coder8124/brain/internal/setup"
)

// npxSetup runs setup as a brain executing from npm's npx cache, after
// prepare has had the fake home, and returns what setup printed and the server
// it registered.
func npxSetup(t *testing.T, prepare func(home string)) (home, out string, got setup.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := setupInFakeHome(t)
	home = os.Getenv("HOME")
	cached := filepath.Join(home, ".npm", "_npx", "2f3ac", "node_modules", "@noeton", "logos", "bin")
	fakeProgram(t, cached, "brain", `echo "brain 0.4.3 darwin/arm64 go1.26"`)
	prepare(home)

	oldExe, oldHosts, oldCheck := executable, detectHosts, integrationChecks
	executable = func() (string, error) { return filepath.Join(cached, "brain"), nil }
	detectHosts = func() []setup.Host {
		return []setup.Host{{
			Name:   "Fakey Desktop",
			Detect: func() bool { return true },
			Where:  func() string { return "/nowhere/config.json" },
			Register: func(s setup.Server) (setup.Outcome, error) {
				got = s
				return setup.Registered, nil
			},
		}}
	}
	integrationChecks = func(string, []string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { executable, detectHosts, integrationChecks = oldExe, oldHosts, oldCheck })

	out = captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	return home, out, got
}

// Setup through npx registered `npx -y @noeton/logos mcp serve`, and npx asks
// npm's registry on every launch — offline it waited 70 seconds and failed, so
// the host had no Logos at all.
func TestAnNpxSetupRegistersACopyOfBrainInsteadOfNpx(t *testing.T) {
	home, out, got := npxSetup(t, func(string) {})

	pin := filepath.Join(home, ".local", "bin", "brain")
	if got.Bin != pin {
		t.Fatalf("registered %q, want the copy at %s:\n%s", got.Bin, pin, out)
	}
	if strings.Join(got.Args, " ") != "mcp serve" {
		t.Errorf("args = %q, want mcp serve", got.Args)
	}
	if !runsAsBrain(pin) {
		t.Errorf("the registered copy at %s does not run as brain", pin)
	}
	if !strings.Contains(out, "copied to "+pin) {
		t.Errorf("setup did not say it copied brain:\n%s", out)
	}
}

// ~/.local/bin/brain may be another program by that name. Overwriting it to
// save a network round trip would break something the user installed.
func TestAnNpxSetupDoesNotOverwriteAnotherProgramCalledBrain(t *testing.T) {
	home := t.TempDir()
	pinDir := filepath.Join(home, ".local", "bin")
	fakeProgram(t, pinDir, "brain", `echo "brain-games 2.1"`)
	before, _ := os.ReadFile(filepath.Join(pinDir, "brain"))

	err := pinBinary("/nonexistent/brain", filepath.Join(pinDir, "brain"))
	if err == nil || !strings.Contains(err.Error(), "not brain") {
		t.Errorf("pinBinary over someone else's brain returned %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(pinDir, "brain")); string(after) != string(before) {
		t.Errorf("another program's brain was overwritten")
	}
}

// When the copy cannot be made, registering its path would give the host a
// server that does not exist; npx still works online, and setup says so.
func TestAnNpxSetupThatCannotCopyFallsBackToNpxAndSaysSo(t *testing.T) {
	_, out, got := npxSetup(t, func(home string) {
		fakeProgram(t, filepath.Join(home, ".local", "bin"), "brain", `echo "brain-games 2.1"`)
	})
	if got.Bin != "npx" {
		t.Errorf("registered %q after the copy failed, want npx", got.Bin)
	}
	if !strings.Contains(out, "could not copy") || !strings.Contains(out, "npx -y @noeton/logos mcp serve") {
		t.Errorf("setup did not say the copy failed and what it registered instead:\n%s", out)
	}
}
