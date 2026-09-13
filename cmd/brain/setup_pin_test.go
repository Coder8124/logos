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
func npxSetup(t *testing.T, prepare func(home string), args ...string) (home, out string, got setup.Server) {
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
		if err := setupCmd(append([]string{"--vault", dir}, args...)); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	return home, out, got
}

// Setup through npx registered `npx -y @noeton/logos mcp serve`, and npx asks
// npm's registry on every launch — offline it waited 70 seconds and failed, so
// the host had no Logos at all.
func TestAnNpxSetupRegistersACopyOfBrainInsteadOfNpx(t *testing.T) {
	home, out, got := npxSetup(t, func(string) {}, "--yes")

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
	}, "--yes")
	if got.Bin != "npx" {
		t.Errorf("registered %q after the copy failed, want npx", got.Bin)
	}
	if !strings.Contains(out, "could not copy") || !strings.Contains(out, "npx -y @noeton/logos mcp serve") {
		t.Errorf("setup did not say the copy failed and what it registered instead:\n%s", out)
	}
}

// withVersion makes this binary report v for the length of the test.
func withVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// withAnswers feeds the prompts setup asks.
func withAnswers(t *testing.T, answers string) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answers); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// newerCopy puts a brain that reports 0.4.9 where setup pins its copy.
func newerCopy(t *testing.T) func(home string) {
	return func(home string) {
		fakeProgram(t, filepath.Join(home, ".local", "bin"), "brain", `echo "brain 0.4.9 darwin/arm64 go1.26"`)
	}
}

func pinnedReports(t *testing.T, home string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".local", "bin", "brain"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// An older `npx @noeton/logos@0.4.3 setup` replaced a 0.4.9 copy without
// asking, silently downgrading every host that launches it. Under --yes there
// is nobody to ask, so the newer copy stays and setup says how to replace it.
func TestAnOlderNpxSetupKeepsANewerPinnedCopyUnderYes(t *testing.T) {
	withVersion(t, "0.4.3")
	home, out, got := npxSetup(t, newerCopy(t), "--yes")

	if !strings.Contains(pinnedReports(t, home), "brain 0.4.9") {
		t.Fatalf("the newer copy was replaced:\n%s", out)
	}
	if got.Bin != filepath.Join(home, ".local", "bin", "brain") {
		t.Errorf("registered %q, want the kept copy", got.Bin)
	}
	if !strings.Contains(out, "0.4.9") || !strings.Contains(out, "--downgrade") {
		t.Errorf("setup did not say it kept 0.4.9 and how to replace it:\n%s", out)
	}
}

// Asked interactively, pressing return must not be the downgrade.
func TestPressingReturnDoesNotDowngradeThePinnedCopy(t *testing.T) {
	withVersion(t, "0.4.3")
	withAnswers(t, "y\n\n")
	home, out, _ := npxSetup(t, newerCopy(t))

	if !strings.Contains(pinnedReports(t, home), "brain 0.4.9") {
		t.Errorf("return at the downgrade prompt replaced the newer copy:\n%s", out)
	}
}

// Downgrading is a choice, so both ways of making it have to work.
func TestAChosenDowngradeReplacesThePinnedCopy(t *testing.T) {
	withVersion(t, "0.4.3")
	for name, run := range map[string]func() (string, string){
		"--downgrade": func() (string, string) {
			home, out, _ := npxSetup(t, newerCopy(t), "--yes", "--downgrade")
			return home, out
		},
		"answering y": func() (string, string) {
			withAnswers(t, "y\ny\n")
			home, out, _ := npxSetup(t, newerCopy(t))
			return home, out
		},
	} {
		home, out := run()
		if !strings.Contains(pinnedReports(t, home), "brain 0.4.3") {
			t.Errorf("%s did not replace the newer copy:\n%s", name, out)
		}
	}
}
