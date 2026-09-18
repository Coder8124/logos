package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/setup"
)

// downloadsSetup runs setup as a release binary still sitting where its archive
// unpacked, with nothing called logos on PATH, and returns what setup printed
// and the server it registered.
func downloadsSetup(t *testing.T, answers string, args ...string) (home, out string, got setup.Server) {
	t.Helper()
	return downloadsSetupWith(t, func(string) string { return "" }, answers, args...)
}

// downloadsSetupWith is downloadsSetup with the home prepared first. prepare
// returns the binary setup is running as, or "" for the unpacked one.
func downloadsSetupWith(t *testing.T, prepare func(home string) string, answers string, args ...string) (home, out string, got setup.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := setupInFakeHome(t)
	home = os.Getenv("HOME")
	// An empty PATH: the whole condition is that this binary cannot be typed,
	// and a logos installed on the machine running the tests would hide it.
	empty := filepath.Join(home, "empty-path")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", empty)

	unpacked := filepath.Join(home, "Downloads", "logos_v0.4.5_darwin_arm64")
	fakeProgram(t, unpacked, "logos", `echo "logos 0.4.5 darwin/arm64 go1.26"`)
	self := prepare(home)
	if self == "" {
		self = filepath.Join(unpacked, "logos")
	}
	withAnswers(t, answers)

	oldExe, oldHosts, oldCheck := executable, detectHosts, integrationChecks
	executable = func() (string, error) { return self, nil }
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
		if err := setupCmd(append([]string{"--vault", dir}, args...)); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	return home, out, got
}

// #89: setup wired every host to ~/Downloads/logos_v0.4.5_darwin_arm64/logos
// and then told the user to move that file onto their PATH so they could type
// `logos`. Doing as they were told broke every host setup had just wired, and
// the hosts named a path that no longer existed. The offer has to come first,
// so what gets wired is the copy that is going to stay where it is.
func TestSetupOffersToCopyABinaryThatIsNotOnPathAndWiresTheCopy(t *testing.T) {
	home, out, got := downloadsSetup(t, "y\ny\ny\n")

	pin := filepath.Join(home, ".local", "bin", "logos")
	if got.Bin != pin {
		t.Fatalf("registered %q, want the copy at %s:\n%s", got.Bin, pin, out)
	}
	if !runsAsLogos(pin) {
		t.Errorf("the registered copy at %s does not run as logos", pin)
	}
	if !strings.Contains(out, "copied to "+pin) {
		t.Errorf("setup did not say it copied logos:\n%s", out)
	}
}

// Declining leaves the binary where it is, and what setup says next has to be
// the instruction that does not break the wiring it just did.
func TestDecliningTheCopyLeavesTheHostsOnTheBinaryWhereItIs(t *testing.T) {
	home, out, got := downloadsSetup(t, "n\ny\ny\n")

	unpacked := filepath.Join(home, "Downloads", "logos_v0.4.5_darwin_arm64", "logos")
	if got.Bin != unpacked {
		t.Errorf("registered %q, want %q — nothing was copied:\n%s", got.Bin, unpacked, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "logos")); err == nil {
		t.Errorf("a copy was made after the offer was declined")
	}
	if !strings.Contains(out, "run `logos setup` again") {
		t.Errorf("setup did not say the hosts must be rewired if the file moves:\n%s", out)
	}
}

// #89's other half. After the user moved the binary onto their PATH, the hosts
// still named the old path — and `logos doctor --integration` answered
// "Working. A host launching this binary reaches this vault", because it built
// the command from the binary it was running rather than reading the one the
// hosts were given. The check exists to answer "can my agents reach this
// vault", so it has to probe what the agents actually launch.
func TestIntegrationProbesTheCommandTheHostsHaveRegistered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := setupInFakeHome(t)
	home := os.Getenv("HOME")
	t.Setenv("LOGOS_VAULT", dir)

	wired := filepath.Join(home, "wired", "logos")
	var probed string
	oldHosts, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host {
		return []setup.Host{{
			Name:   "Fakey Desktop",
			Detect: func() bool { return true },
			Where:  func() string { return "/nowhere/config.json" },
			List: func() ([]setup.Registration, error) {
				return []setup.Registration{{Name: "logos", Command: wired + " mcp serve", Vault: dir}}, nil
			},
		}}
	}
	integrationChecks = func(bin string, _ []string, _ string) []health.Check {
		probed = bin
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = oldHosts, oldCheck })

	out := captureStdout(t, func() {
		if err := doctorIntegration(); err != nil {
			t.Fatalf("doctor --integration: %v", err)
		}
	})

	if probed != wired {
		t.Errorf("probed %q, but Fakey Desktop launches %q:\n%s", probed, wired, out)
	}
	if !strings.Contains(out, "Fakey Desktop") {
		t.Errorf("the host whose command was probed was not named:\n%s", out)
	}
}

// Two hosts commonly register the identical command with different vaults —
// that split is the reason this check exists. Deduping on the command alone
// dropped the second host's probe, so the wrong vault was never launched and
// doctor closed with "Working".
func TestIntegrationProbesTwoHostsOnTheSameCommandWithDifferentVaults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := setupInFakeHome(t)
	home := os.Getenv("HOME")
	t.Setenv("LOGOS_VAULT", dir)
	elsewhere := filepath.Join(home, "other-vault")

	wired := filepath.Join(home, "wired", "logos")
	host := func(name, vault string) setup.Host {
		return setup.Host{
			Name:   name,
			Detect: func() bool { return true },
			Where:  func() string { return "/nowhere/config.json" },
			List: func() ([]setup.Registration, error) {
				return []setup.Registration{{Name: "logos", Command: wired + " mcp serve", Vault: vault}}, nil
			},
		}
	}
	var probed []string
	oldHosts, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host { return []setup.Host{host("Fakey Desktop", dir), host("Fakey Code", elsewhere)} }
	integrationChecks = func(_ string, _ []string, vault string) []health.Check {
		probed = append(probed, vault)
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = oldHosts, oldCheck })

	out := captureStdout(t, func() {
		if err := doctorIntegration(); err != nil {
			t.Fatalf("doctor --integration: %v", err)
		}
	})

	if len(probed) != 2 || probed[1] != elsewhere {
		t.Errorf("probed %v, want both vaults including %s:\n%s", probed, elsewhere, out)
	}
}

// A host that cannot say what it has registered is not a host with nothing
// registered. Dropping the error left doctor probing the command setup would
// write and closing with "Working" — a success-shaped result for a question
// nobody managed to ask (invariant 4).
func TestIntegrationSaysWhichHostsRegistrationsItCouldNotRead(t *testing.T) {
	dir := setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", dir)

	oldHosts, oldCheck := detectHosts, integrationChecks
	detectHosts = func() []setup.Host {
		return []setup.Host{{
			Name:   "Fakey Desktop",
			Detect: func() bool { return true },
			Where:  func() string { return "/nowhere/config.json" },
			List:   func() ([]setup.Registration, error) { return nil, errors.New("config.json is not readable") },
		}}
	}
	integrationChecks = func(string, []string, string) []health.Check {
		return []health.Check{{Name: "handshake", State: health.OK, Detail: "faked"}}
	}
	t.Cleanup(func() { detectHosts, integrationChecks = oldHosts, oldCheck })

	var out string
	err := func() error {
		var e error
		out = captureStdout(t, func() { e = doctorIntegration() })
		return e
	}()
	if err == nil {
		t.Errorf("doctor reported success having failed to read the only host's registrations:\n%s", out)
	}
	if !strings.Contains(out, "config.json is not readable") || !strings.Contains(out, "Fakey Desktop") {
		t.Errorf("the host whose registrations could not be read was not named:\n%s", out)
	}
}

// A logos already in ~/.local/bin may be newer than the one being set up — a
// release archive from before an upgrade, run once more out of Downloads. The
// copy was made before anything compared versions, so it overwrote the newer
// install and every host was quietly downgraded. Under --yes there is nobody
// to ask, so the newer one stays and is the one the hosts are wired to.
func TestSetupDoesNotReplaceANewerLogosInLocalBinUnderYes(t *testing.T) {
	withVersion(t, "0.4.5")
	home, out, got := downloadsSetupWith(t, func(home string) string {
		fakeProgram(t, filepath.Join(home, ".local", "bin"), "logos", `echo "logos 0.4.9 darwin/arm64 go1.26"`)
		return ""
	}, "", "--yes")

	pin := filepath.Join(home, ".local", "bin", "logos")
	raw, err := os.ReadFile(pin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "logos 0.4.9") {
		t.Fatalf("the newer logos at %s was replaced by this 0.4.5:\n%s", pin, out)
	}
	if got.Bin != pin {
		t.Errorf("registered %q, want the kept copy at %s:\n%s", got.Bin, pin, out)
	}
	if !strings.Contains(out, "--downgrade") {
		t.Errorf("setup did not say how to replace it:\n%s", out)
	}
}

// Homebrew's logos updates itself. Copying it into ~/.local/bin freezes it at
// today's version and puts that copy ahead of Homebrew on PATH, so the hosts
// launch the one install `brew upgrade` can never reach — the same trap #83 is
// about. A managed install is wired where it stands.
func TestSetupDoesNotCopyAManagedInstallIntoLocalBin(t *testing.T) {
	var cellar string
	home, out, got := downloadsSetupWith(t, func(home string) string {
		cellar = filepath.Join(home, "homebrew", "Cellar", "logos-mcp", "0.4.5", "bin")
		fakeProgram(t, cellar, "logos", `echo "logos 0.4.5 darwin/arm64 go1.26"`)
		return filepath.Join(cellar, "logos")
	}, "y\ny\ny\n", "--yes")

	if got.Bin != filepath.Join(cellar, "logos") {
		t.Errorf("registered %q, want Homebrew's own %s:\n%s", got.Bin, cellar, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "logos")); err == nil {
		t.Errorf("Homebrew's logos was copied into ~/.local/bin:\n%s", out)
	}
}

// Having copied logos into ~/.local/bin, setup closed by telling the user to
// move it onto their PATH and re-run setup — the very instruction #89 exists
// about, and this time pointed at the copy the hosts had just been wired to.
// The directory is what needs to be on PATH, not the file somewhere else.
func TestAfterTheCopyTheClosingHintDoesNotSayToMoveIt(t *testing.T) {
	home, out, _ := downloadsSetup(t, "y\ny\ny\n")

	if strings.Contains(out, "run `logos setup` again") {
		t.Errorf("setup told the user to move the copy it just wired the hosts to:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(home, ".local", "bin")+" to your PATH") {
		t.Errorf("setup did not say which directory to put on PATH:\n%s", out)
	}
}

// --dry-run skipped the offer entirely, so the roster showed the hosts being
// pointed at ~/Downloads and nothing said that a real run writes a binary into
// ~/.local/bin and wires them there instead. A plan that omits the one file the
// run creates is not the plan.
func TestDryRunSaysARealRunWouldCopyLogosOntoThePath(t *testing.T) {
	home, out, _ := downloadsSetup(t, "", "--dry-run")

	pin := filepath.Join(home, ".local", "bin", "logos")
	if !strings.Contains(out, pin) {
		t.Errorf("--dry-run never mentioned the copy at %s:\n%s", pin, out)
	}
	if _, err := os.Stat(pin); err == nil {
		t.Errorf("--dry-run wrote %s", pin)
	}
}
