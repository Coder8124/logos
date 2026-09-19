package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/setup"
)

// brewAndPin builds the migration this bug is about: a copy setup pinned into
// ~/.local/bin back when the install was npx, and a later `brew install` that
// the pin now shadows on PATH. Returns the pin and Homebrew's opt path.
func brewAndPin(t *testing.T) (pin, opt string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binaries are shell scripts")
	}
	home := os.Getenv("HOME")
	prefix := filepath.Join(home, "homebrew")
	cellar := filepath.Join(prefix, "Cellar", "logos-mcp", "0.4.3", "bin")
	fakeProgram(t, cellar, "logos", `echo "logos 0.4.3 darwin/arm64 go1.26"`)
	optDir := filepath.Join(prefix, "opt", "logos-mcp", "bin")
	if err := os.MkdirAll(optDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cellar, "logos"), filepath.Join(optDir, "logos")); err != nil {
		t.Fatal(err)
	}
	pinDir := filepath.Join(home, ".local", "bin")
	fakeProgram(t, pinDir, "logos", `echo "logos 0.4.2 darwin/arm64 go1.26"`)

	old := health.HomebrewPrefixes
	health.HomebrewPrefixes = func() []string { return []string{prefix} }
	t.Cleanup(func() { health.HomebrewPrefixes = old })

	// On PATH, so the copy is not also the subject of #89's "not on your PATH"
	// offer — this test is about which of two installs the hosts are given.
	t.Setenv("PATH", pinDir)
	return filepath.Join(pinDir, "logos"), filepath.Join(optDir, "logos")
}

// pinSetup runs setup from bin and returns what it printed and what it wired.
func pinSetup(t *testing.T, bin, vault, answers string, args ...string) (out string, got setup.Server) {
	t.Helper()
	withAnswers(t, answers)
	oldExe, oldHosts, oldCheck := executable, detectHosts, integrationChecks
	executable = func() (string, error) { return bin, nil }
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
		if err := setupCmd(append([]string{"--vault", vault}, args...)); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	return out, got
}

// The pin is the copy setup itself made; nothing on disk said so, so setup
// could neither prefer the install that replaced it nor clean it up. The
// receipt is that record (#83).
func TestThePinnedCopySetupMakesLeavesAReceiptSayingItIsSetupsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	setupInFakeHome(t)
	home := os.Getenv("HOME")
	fakeProgram(t, filepath.Join(home, "src"), "logos", `echo "logos 0.4.5 darwin/arm64 go1.26"`)
	dst := filepath.Join(home, ".local", "bin", "logos")

	if err := pinBinary(filepath.Join(home, "src", "logos"), dst); err != nil {
		t.Fatal(err)
	}
	if !ourPin(dst) {
		t.Errorf("setup's own copy at %s is not recognisable as its own", dst)
	}

	// A logos somebody else put there is not setup's to claim or remove.
	theirs := filepath.Join(home, "elsewhere", "bin", "logos")
	fakeProgram(t, filepath.Dir(theirs), "logos", `echo "logos 0.4.5 darwin/arm64 go1.26"`)
	if ourPin(theirs) {
		t.Errorf("%s was claimed as setup's own copy", theirs)
	}
}

// The migration in the report: npx first, then `brew install`. `logos` in a
// shell still runs the pin, so `logos setup` runs from the pin and used to
// wire every host to it — the one install `brew upgrade` can never reach.
func TestSetupRunFromItsOwnPinnedCopyWiresHomebrewsLogosInstead(t *testing.T) {
	vault := setupInFakeHome(t)
	pin, opt := brewAndPin(t)
	if err := writePinReceipt(pin); err != nil {
		t.Fatal(err)
	}
	withVersion(t, "v0.4.2")

	out, got := pinSetup(t, pin, vault, "", "--yes")

	if got.Bin != opt {
		t.Fatalf("registered %q, want Homebrew's %s:\n%s", got.Bin, opt, out)
	}
	if !strings.Contains(out, opt) {
		t.Errorf("setup did not say which logos it wired:\n%s", out)
	}
}

// Setup run from Homebrew already named the pin ahead of it on PATH and told
// the user to delete it by hand. It made that copy; it can offer to take it
// back, which is the difference between a warning and a migration.
func TestSetupRunFromHomebrewOffersToRemoveTheCopyItPinned(t *testing.T) {
	vault := setupInFakeHome(t)
	pin, opt := brewAndPin(t)
	if err := writePinReceipt(pin); err != nil {
		t.Fatal(err)
	}
	withVersion(t, "v0.4.3")

	out, _ := pinSetup(t, opt, vault, "y\ny\ny\ny\n")

	if _, err := os.Stat(pin); !os.IsNotExist(err) {
		t.Errorf("the old copy at %s is still there:\n%s", pin, out)
	}
	if !strings.Contains(out, "removed "+pin) {
		t.Errorf("setup did not say it removed the old copy:\n%s", out)
	}
}
