package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each of these used to fall through to the default ~/logos vault and wire it
// into every host with no warning: flagStr only matched `--vault value`, and
// nothing rejected a word it did not know. A user who typed a path read the
// vault line as confirmation of the path they typed.
func TestSetupRefusesAMistypedOrValuelessFlagBeforeTouchingAnything(t *testing.T) {
	for _, args := range [][]string{
		{"--vualt", "~/notes", "--yes"},
		{"--vault", "--yes"},
		{"--yes", "--vault"},
		{"--vault=", "--yes"},
		{"--host=", "--yes"},
	} {
		dir := setupInFakeHome(t)
		fakeHosts(t, "Cursor")
		var err error
		captureStdout(t, func() { err = setupCmd(args) })
		if err == nil {
			t.Errorf("setup %v: no error", args)
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("setup %v created the default vault at %s", args, dir)
		}
	}
}

func TestMcpInstallRefusesAnUnknownFlag(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Cursor")
	err := mcpInstallCmd([]string{"install", "--hots", "cursor", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "--hots") {
		t.Errorf("mcp install with --hots: got %v, want an error naming the flag", err)
	}
}

func TestSetupAcceptsTheEqualsSpellingOfAValueFlag(t *testing.T) {
	setupInFakeHome(t)
	fakeHosts(t, "Cursor")
	want := filepath.Join(t.TempDir(), "notes")
	captureStdout(t, func() {
		if err := setupCmd([]string{"--vault=" + want, "--no-hosts", "--dry-run", "--yes"}); err != nil {
			t.Errorf("setup --vault=%s: %v", want, err)
		}
	})
	got, err := normalizeSetupFlags([]string{"--vault=" + want, "--host=cursor,codex", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if v := flagStr(got, "--vault", ""); v != want {
		t.Errorf("--vault= resolved to %q, want %q", v, want)
	}
	if h := flagStrs(got, "--host"); len(h) != 2 || h[0] != "cursor" || h[1] != "codex" {
		t.Errorf("--host= resolved to %v", h)
	}
}
