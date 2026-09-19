package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/setup"
)

// When the copy into ~/.local/bin fails, the hosts are rewired to npx — but the
// closing line still described the copy, so setup ended by handing the user a
// path to a file it had just said it could not write, and telling them to move
// it. The command printed has to be the command the run actually left behind.
func TestAnNpxSetupThatCannotCopyDoesNotCloseByNamingTheCopy(t *testing.T) {
	home, out, _ := npxSetup(t, func(home string) {
		fakeProgram(t, filepath.Join(home, ".local", "bin"), "logos", `echo "logos-games 2.1"`)
	}, "--yes")

	pin := filepath.Join(home, ".local", "bin", "logos")
	if strings.Contains(out, pin+" resume") || strings.Contains(out, "move it into a directory") {
		t.Errorf("setup closed by naming the copy it could not make:\n%s", out)
	}
	if !strings.Contains(out, "npx @noeton/logos") {
		t.Errorf("setup did not close with the command it actually wired:\n%s", out)
	}
}

// pinnedHome puts HOME somewhere with a space in it and leaves the receipt that
// marks ~/.local/bin/logos as setup's own copy. It returns that copy's path.
func pinnedHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Jo Smith")
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	pin := filepath.Join(binDir, "logos")
	if err := os.WriteFile(filepath.Join(binDir, ".logos-pin"), []byte(pin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return pin
}

// The pinned-copy branch returned before the quoting below it, so a home
// directory with a space in it closed setup with a command the shell splits
// into two words — in the one case the user is most likely to paste it.
func TestTheCommandForAPinnedCopyIsQuotedWhenItsPathHasASpace(t *testing.T) {
	pin := pinnedHome(t)
	t.Setenv("PATH", t.TempDir())

	cmd, hint := terminalCommand(pin)
	if !strings.HasPrefix(cmd, "'") {
		t.Errorf("command = %q, want it quoted for a path with a space", cmd)
	}
	if !strings.Contains(hint, "PATH") {
		t.Errorf("hint = %q, want it to say to put the directory on PATH", hint)
	}
}

// Setup run from its own pinned copy, with ~/.local/bin not on PATH, asked
// "copy it to ~/.local/bin/logos and wire the hosts to the copy?" about the
// file it was already running as, and then reported copying it there.
func TestSetupDoesNotOfferToCopyItsOwnPinOntoItself(t *testing.T) {
	pin := pinnedHome(t)
	t.Setenv("PATH", t.TempDir())
	old := executable
	executable = func() (string, error) { return pin, nil }
	t.Cleanup(func() { executable = old })

	var got string
	out := captureStdout(t, func() { got, _ = offerPathCopy(true, false) })
	if got != "" {
		t.Errorf("offered to copy %s onto itself:\n%s", pin, out)
	}
	if strings.Contains(out, "copy it to") {
		t.Errorf("setup asked about copying a file onto itself:\n%s", out)
	}
}

// The prose above the roster said a real run copies the binary, but the roster
// itself — the block the user says yes or no to — still named the binary in
// ~/Downloads. The plan has to name what a real run would wire.
func TestDryRunsRosterNamesTheCopyTheRealRunWouldWire(t *testing.T) {
	home, out, _ := downloadsSetup(t, "", "--dry-run")

	pin := filepath.Join(home, ".local", "bin", "logos")
	if !strings.Contains(out, pin+" mcp serve") {
		t.Errorf("the roster did not name the copy a real run wires:\n%s", out)
	}
	if _, err := os.Stat(pin); err == nil {
		t.Errorf("--dry-run wrote %s", pin)
	}
}

// One unreadable host among several is not "no host's registrations could be
// read". Said that way, a user whose other hosts were read perfectly well is
// sent to check a permission problem that does not exist.
func TestDoctorNamesTheUnreadableHostWithoutClaimingNoneCouldBeRead(t *testing.T) {
	dir := setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", dir)

	oldHosts, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host {
		return []setup.Host{
			{
				Name:   "Fakey Desktop",
				Detect: func() bool { return true },
				Where:  func() string { return "/nowhere/config.json" },
				// Read fine; it simply has no logos in it.
				List: func() ([]setup.Registration, error) { return nil, nil },
			},
			{
				Name:   "Fakey Code",
				Detect: func() bool { return true },
				Where:  func() string { return "/nowhere/codex.toml" },
				List:   func() ([]setup.Registration, error) { return nil, errors.New("codex.toml is not readable") },
			},
		}
	}
	integrationChecks = func(string, []string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = oldHosts, oldCheck })

	var err error
	out := captureStdout(t, func() { err = doctorIntegration() })
	if err == nil {
		t.Fatalf("doctor reported success with a host it could not read:\n%s", out)
	}
	if strings.Contains(err.Error(), "no host's registrations could be read") {
		t.Errorf("doctor said no host could be read when Fakey Desktop was read fine: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Fakey Code") {
		t.Errorf("the unreadable host was not named:\n%s", out)
	}
}
