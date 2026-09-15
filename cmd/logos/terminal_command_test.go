package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup ended with "From the terminal: logos resume <project>" whatever the
// install. Under npx there is no logos on PATH, and a binary run from a clone
// or from Downloads is not on PATH either — the first command a new user was
// handed answered "command not found".
func TestTheTerminalCommandUnderNpxIsNpx(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cmd, hint := terminalCommand("/Users/someone/.npm/_npx/c2f5/node_modules/@noeton/logos-darwin-arm64/bin/logos")
	if cmd != "npx @noeton/logos" {
		t.Errorf("command = %q, want npx @noeton/logos", cmd)
	}
	if !strings.Contains(hint, "npm i -g @noeton/logos") {
		t.Errorf("an npx user should be told how to get a logos command, got %q", hint)
	}
}

func TestTheTerminalCommandIsLogosWhenLogosOnPathIsThisBinary(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "logos")
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	resolved, _ := filepath.EvalSymlinks(self)
	if cmd, hint := terminalCommand(resolved); cmd != "logos" || hint != "" {
		t.Errorf("got %q, %q; want logos and no hint", cmd, hint)
	}
}

// A different logos earlier on PATH would run a different version against the
// same vault, so it is not offered as this one.
func TestABinaryNotOnPathIsNamedByItsFullPath(t *testing.T) {
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "logos"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	self := "/Users/someone/Downloads/logos_0.4.3_darwin_arm64/logos"
	cmd, hint := terminalCommand(self)
	if cmd != self {
		t.Errorf("command = %q, want the full path %q", cmd, self)
	}
	if !strings.Contains(hint, "PATH") {
		t.Errorf("the hint should say logos is not on PATH, got %q", hint)
	}
}

// The hint used to say only "move it into a directory that is". Setup had
// just wired every host to the path it was run from, so doing as told left
// every host launching a file that was no longer there.
func TestThePathHintSaysMovingLogosMeansRunningSetupAgain(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, hint := terminalCommand("/Users/someone/Downloads/logos_0.4.3_darwin_arm64/logos")
	if !strings.Contains(hint, "setup` again") {
		t.Errorf("the hint must say to run setup again after moving logos, got %q", hint)
	}
}

// The closing line itself: this test binary is nowhere on PATH, exactly like a
// release binary run from Downloads, so it must not be told to type `logos`.
func TestSetupDoesNotSuggestLogosWhenLogosIsNotOnPath(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")
	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if strings.Contains(out, "From the terminal: logos resume") {
		t.Errorf("setup suggested a logos command that is not on PATH:\n%s", out)
	}
	if !strings.Contains(out, "not on your PATH") {
		t.Errorf("setup should say logos is not on PATH:\n%s", out)
	}
}
