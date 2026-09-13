package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The CLI is what every host config starts, so the old variable has to be
// honoured before any command reads it — and said out loud, or nobody renames it
// before 0.5.0 drops the old name.
func TestTheCLIReadsBrainVariablesAndSaysSo(t *testing.T) {
	t.Setenv("BRAIN_VAULT", "/somewhere/vault")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")

	var stderr bytes.Buffer
	carryOldNames(&stderr)

	if v := os.Getenv("LOGOS_VAULT"); v != "/somewhere/vault" {
		t.Errorf("LOGOS_VAULT = %q after start, want the BRAIN_VAULT value", v)
	}
	if !strings.Contains(stderr.String(), "BRAIN_VAULT") || !strings.Contains(stderr.String(), "LOGOS_VAULT") {
		t.Errorf("start did not name the old variable and its new name:\n%s", stderr.String())
	}
}

// An older brain binary is still on PATH for some plugin users, and it reads
// only the BRAIN_ names. Without them its session-end note ignores the
// uncommitted-only rule and is signed as cli.
func TestTheSessionEndHookSetsBothNamesForAnOlderBinary(t *testing.T) {
	raw, err := os.ReadFile("../../plugin/hooks/session-end.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, `note "$project"`) {
			continue
		}
		for _, want := range []string{"BRAIN_AGENT=claude-code", "BRAIN_NOTE_IF_UNCOMMITTED=1", "LOGOS_AGENT=claude-code", "LOGOS_NOTE_IF_UNCOMMITTED=1"} {
			if !strings.Contains(line, want) {
				t.Errorf("the note line does not set %s:\n%s", want, line)
			}
		}
		return
	}
	t.Fatal("no note line found in session-end.sh")
}

// Recording the old vault is only half of it; a CLI that did so silently would
// leave someone wondering why logos is reading ~/brain.
func TestTheCLIAnnouncesCarryingTheOldVault(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", h+"/.config")
	t.Setenv("LOGOS_VAULT", "")
	os.Unsetenv("LOGOS_VAULT")
	if err := os.Mkdir(h+"/brain", 0o700); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	carryOldNames(&stderr)

	if !strings.Contains(stderr.String(), h+"/brain") {
		t.Errorf("start did not name the old vault it is using:\n%s", stderr.String())
	}
}

// The move has to happen before any command opens the index, or that command
// builds a fresh .logos/ and the old one is left over for good.
func TestTheCLIMovesTheOldStateDirectoryBeforeAnyCommand(t *testing.T) {
	v := t.TempDir()
	t.Setenv("LOGOS_VAULT", v)
	if err := os.Mkdir(v+"/.brain", 0o700); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	carryOldNames(&stderr)

	if _, err := os.Stat(v + "/.logos"); err != nil {
		t.Errorf(".logos is missing after start: %v", err)
	}
	if !strings.Contains(stderr.String(), "moved") {
		t.Errorf("start did not announce the move:\n%s", stderr.String())
	}
}
