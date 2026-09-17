package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/setup"
	"github.com/Coder8124/logos/internal/vault"
)

// `setup --vault /tmp/try-logos` recorded the directory as this machine's vault
// and said so cheerfully; the next `logos doctor` failed it as a temporary
// directory. Setup is where the check prevents the problem, so it asks there.
func TestSetupDoesNotRecordATemporaryVaultWithoutBeingTold(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")
	withAnswers(t, "")

	scratch := filepath.Join(t.TempDir(), "try-logos")
	var rec recordOutcome
	captureStdout(t, func() {
		var err error
		_, _, rec, err = chooseVault([]string{"--vault", scratch}, false)
		if err != nil {
			t.Fatal(err)
		}
	})
	if got := vault.Recorded(); got != "" {
		t.Errorf("a temporary directory was recorded as this machine's vault: %q", got)
	}
	if rec != recordSkipTemp {
		t.Errorf("record outcome = %v, want recordSkipTemp so setup says why nothing was recorded", rec)
	}
}

// Declining is the default, not the only answer: --record-temp records it, for
// the person who really does want a throwaway vault to be the machine's.
func TestSetupRecordsATemporaryVaultWhenToldTo(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")

	scratch := filepath.Join(t.TempDir(), "try-logos")
	captureStdout(t, func() {
		if _, _, _, err := chooseVault([]string{"--vault", scratch, "--record-temp"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if got := vault.Recorded(); got != scratch {
		t.Errorf("recorded vault = %q, want %q after --record-temp", got, scratch)
	}
}

// `--yes` means "do not ask me questions", and a user passing it in a script
// got "record this scratch directory as the machine's permanent vault" as well.
// The pointer is one file, and that is how it moved without anyone deciding to
// move it. --yes answers questions; it does not waive the guard.
func TestYesDoesNotRecordATemporaryVault(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("LOGOS_VAULT", "")

	scratch := filepath.Join(t.TempDir(), "try-logos")
	var rec recordOutcome
	out := captureStdout(t, func() {
		var err error
		if _, _, rec, err = chooseVault([]string{"--vault", scratch, "--yes"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if got := vault.Recorded(); got != "" {
		t.Errorf("--yes recorded a temporary directory as this machine's vault: %q", got)
	}
	if rec != recordSkipTemp {
		t.Errorf("record outcome = %v, want recordSkipTemp", rec)
	}
	if !strings.Contains(out, "--record-temp") {
		t.Errorf("nothing said how to record it deliberately:\n%s", out)
	}
}

// Declining to record a temporary vault printed "used for this run only", and
// setup then wrote that path into every host it wired, where it outlives the run.
func TestATemporaryVaultNobodyRecordedIsNotWiredIntoHosts(t *testing.T) {
	setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", "")
	// No to recording it, yes to wiring the hosts.
	withAnswers(t, "n\ny\n")
	wired := false
	fakeHosts(t, "Fakey Cursor")
	hosts := detectHosts()
	hosts[0].Register = func(setup.Server) (setup.Outcome, error) { wired = true; return setup.Registered, nil }
	detectHosts = func() []setup.Host { return hosts }

	scratch := filepath.Join(os.TempDir(), "try-logos")
	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", scratch}) })

	if wired {
		t.Errorf("a vault for this run only was wired into a host:\n%s", out)
	}
	// An error, not a note: a script with no terminal must not read a run that
	// wired nothing as a success.
	if err == nil || !strings.Contains(err.Error(), "no hosts were wired") {
		t.Errorf("setup did not fail saying the hosts were left alone: err=%v\n%s", err, out)
	}
}

// Declining to record a temporary vault that is already the recorded one
// changes nothing, so its hosts are wired as before.
func TestATemporaryVaultAlreadyRecordedIsStillWiredIntoHosts(t *testing.T) {
	setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", "")
	withAnswers(t, "n\ny\n")
	wired := false
	fakeHosts(t, "Fakey Cursor")
	hosts := detectHosts()
	hosts[0].Register = func(setup.Server) (setup.Outcome, error) { wired = true; return setup.Registered, nil }
	detectHosts = func() []setup.Host { return hosts }

	scratch := filepath.Join(os.TempDir(), "try-logos")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.Record(scratch); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureStdout(t, func() { err = setupCmd([]string{"--vault", scratch}) })

	if err != nil || !wired {
		t.Errorf("the recorded vault's hosts were not wired: err=%v\n%s", err, out)
	}
}

// `go run ./cmd/logos setup` runs a binary Go deletes when the command exits.
// Every host was wired to it and every check passed, because it still existed
// during the run.
func TestSetupRefusesToWireABinaryGoRunWillDelete(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Cursor")
	old := executable
	executable = func() (string, error) { return "/private/tmp/go-build4043778508/b001/exe/logos", nil }
	t.Cleanup(func() { executable = old })

	var err error
	captureStdout(t, func() { err = wireHosts(dir, wireOpts{}) })

	if err == nil || !strings.Contains(err.Error(), "go build") {
		t.Fatalf("setup wired a go run binary: err=%v", err)
	}
}
