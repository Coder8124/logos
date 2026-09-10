package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pointing BRAIN_VAULT at a path that does not exist is either a typo or a
// machine where `brain setup` has not run yet. Every command has to say so.
// `brain announce quiet` and `brain think medium` did not: they created the
// directory tree and wrote the setting into it, reported success, and left a
// half-vault behind that the real vault knew nothing about. A user changing a
// setting before setup had no way to learn it had not landed.
func TestACommandAgainstAVaultThatIsNotThereSaysSoAndCreatesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(dir string) error
	}{
		{"announce <level>", func(string) error { return runAnnounce([]string{"quiet"}) }},
		{"announce", func(string) error { return runAnnounce(nil) }},
		{"think <level>", func(string) error { return runThink("medium") }},
		{"think", func(string) error { return runThink("") }},
		{"plans", func(string) error { return runPlans([]string{"someproject"}) }},
		{"activity", func(string) error { return runActivity(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "not-a-vault")
			t.Setenv("BRAIN_VAULT", dir)

			var err error
			captureStdout(t, func() { err = tc.run(dir) })

			if err == nil {
				t.Fatalf("succeeded against a vault that does not exist")
			}
			if !strings.Contains(err.Error(), "vault not found") || !strings.Contains(err.Error(), dir) {
				t.Errorf("error does not name the missing vault: %v", err)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Errorf("a command that failed still created %s", dir)
			}
		})
	}
}

// `brain doctor` is the command a script or a pre-flight check runs to decide
// whether this machine is usable. It printed "1 failed" and exited 0, which is
// the success-shaped failure invariant 4 is about: anything reading the exit
// code concluded the vault was fine.
func TestDoctorExitsNonZeroWhenACheckFailed(t *testing.T) {
	t.Setenv("BRAIN_VAULT", filepath.Join(t.TempDir(), "not-a-vault"))

	var err error
	out := captureStdout(t, func() { err = doctor(false) })

	if !strings.Contains(out, "FAILED") {
		t.Fatalf("expected a failing check to report on:\n%s", out)
	}
	if err == nil {
		t.Fatal("doctor reported a failed check and still succeeded")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error does not say what happened: %v", err)
	}
}

// A search with no hits printed nothing at all — indistinguishable from a
// command that crashed before it got started. `brain ask` says so in words and
// search now does too.
func TestASearchWithNoHitsSaysSoRatherThanPrintingNothing(t *testing.T) {
	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_EMBED", "off") // lexical only: no runtime in a test

	var err error
	out := captureStdout(t, func() { err = search("nothing-here-matches-this") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing in the vault matches") {
		t.Errorf("a zero-hit search said nothing:\n%q", out)
	}
}
