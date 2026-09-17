package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bug report that arrives as "it doesn't work" costs a round trip to answer,
// and the round trip is where most reports die. The user cannot be asked to
// send diagnostics we collect ourselves — invariant 5 — so the only route is
// one command that prints what a maintainer would have asked for, and that the
// user can read before pasting it.
func TestTheSupportReportNamesTheThingsTheFirstReplyWouldAskFor(t *testing.T) {
	home := t.TempDir()
	vault := filepath.Join(home, "logos")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("LOGOS_VAULT", vault)

	out := captureStdout(t, func() {
		if err := doctorReport(); err != nil {
			t.Fatal(err)
		}
	})

	for _, want := range []string{
		"logos",        // the version
		runtimeLabel,   // os/arch, since half the open bugs are one platform
		"vault",        // which vault, and whether it is there
		"binaries",     // which copies exist: two logos on PATH explains most of them
		"hosts",        // the wiring, which is what setup actually changed
		"plugin",       // the plugin version, which lags the binary on purpose
		"no telemetry", // said out loud, because the user is about to paste this
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report never mentions %q:\n%s", want, out)
		}
	}
}

// The user pastes this into a public issue. A home directory carries their
// name, and on a work machine the vault path can carry an employer's too —
// neither is needed to answer the question, so neither is printed.
func TestTheSupportReportDoesNotCarryTheUsersHomeDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hjalmar-borgstrom")
	vault := filepath.Join(home, "logos")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("LOGOS_VAULT", vault)

	out := captureStdout(t, func() {
		if err := doctorReport(); err != nil {
			t.Fatal(err)
		}
	})

	if strings.Contains(out, home) {
		t.Errorf("the report pastes the user's home directory into a public issue:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join("~", "logos")) {
		t.Errorf("the vault path was redacted away entirely; it should read as ~/logos:\n%s", out)
	}
}
