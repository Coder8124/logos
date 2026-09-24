package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// #163: `note --agent A "text"` filed the note under a project called "agent"
// and printed `noted`. A flag the command does not know has to be refused by
// name before anything is written.
func TestANoteGivenAFlagIsRefusedRatherThanFiledUnderAProjectNamedAfterIt(t *testing.T) {
	err := checkCommandFlags("note", []string{"--agent", "A", "starting the importer"})
	if err == nil || !strings.Contains(err.Error(), `unknown flag "--agent"`) {
		t.Fatalf("note --agent should be refused by name, got %v", err)
	}
	// A single dash is still a legitimate project or note.
	if err := checkCommandFlags("note", []string{"-", "a note about stdin"}); err != nil {
		t.Errorf("a lone dash is a value, not a flag: %v", err)
	}
}

// #164: the read commands ran normally, exit 0, with a flag they did not know —
// `tried "x" --bogus` answered "nothing rules this out" against the wrong scope.
func TestReadCommandsRefuseAFlagTheyDoNotKnow(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
	}{
		{"resume", []string{"--bogus"}},
		{"resume", []string{"--project"}},
		{"sessions", []string{"--bogus"}},
		{"index", []string{"--bogus"}},
		{"version", []string{"--bogus"}},
		{"doctor", []string{"--bogus"}},
		{"tried", []string{"x", "--bogus"}},
		{"why", []string{"a.go", "--bogus"}},
		{"graph", []string{"--bogus"}},
		{"replay", []string{"--bogus"}},
	} {
		if err := checkCommandFlags(c.cmd, c.args); err == nil {
			t.Errorf("logos %s %s ran as though the flag were not there", c.cmd, strings.Join(c.args, " "))
		}
	}
}

// #164: a number the user gave and the command cannot use fell back to the
// default in silence. --budget -5 is a value that was typed, not one left out.
func TestANumericFlagWithAnUnusableValueIsRefused(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
	}{
		{"context", []string{"task", "--budget", "abc"}},
		{"context", []string{"task", "--budget", "-5"}},
		{"resume", []string{"--budget", "abc"}},
		{"why", []string{"a.go", "--limit", "-1"}},
		{"why", []string{"a.go", "--limit", "abc"}},
		{"graph", []string{"--hops", "abc"}},
		{"graph", []string{"--hops", "-1"}},
	} {
		if err := checkCommandFlags(c.cmd, c.args); err == nil {
			t.Errorf("logos %s %s accepted a value it cannot use", c.cmd, strings.Join(c.args, " "))
		}
	}
}

// 0 is the documented way to ask for the default budget; refusing it left a
// script with no way to say "whatever you normally use".
func TestABudgetOfZeroAsksForTheDefaultRatherThanBeingRefused(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
	}{
		{"context", []string{"task", "--budget", "0"}},
		{"context", []string{"task", "-b", "0"}},
		{"resume", []string{"brain", "--budget", "0"}},
	} {
		if err := checkCommandFlags(c.cmd, c.args); err != nil {
			t.Errorf("logos %s %s: %v", c.cmd, strings.Join(c.args, " "), err)
		}
	}
}

// The flags each command does know, as the docs and hooks spell them, keep
// working — including #116's missing-value fallback.
func TestTheFlagsACommandDocumentsStillPass(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
	}{
		{"resume", []string{"brain", "--budget", "2000", "--since", "week"}},
		{"resume", []string{"brain", "--budget"}},
		{"context", []string{"cut the BOM", "--project", "kestrel", "--budget", "4000", "--since", "week"}},
		{"context", []string{"--pin", "sessions/old"}},
		{"context", []string{"--rules"}},
		{"doctor", []string{"--verbose", "--probe"}},
		{"doctor", []string{"--integration"}},
		{"tried", []string{"x", "--ruled-out", "it deadlocks", "--layer", "design", "--instead", "y"}},
		{"why", []string{"a.go", "--limit", "3"}},
		{"why", []string{"a.go", "-n", "3"}},
		{"graph", []string{"focus", "--hops", "3", "--similar"}},
		{"graph", []string{"focus", "--hops", "0"}},
		{"sessions", []string{"brain", "--close", "abc"}},
		{"replay", []string{"--peek"}},
		{"index", []string{"--watch"}},
		{"note", []string{"brain", "fixed the --verbose flag"}},
	} {
		if err := checkCommandFlags(c.cmd, c.args); err != nil {
			t.Errorf("logos %s %s: %v", c.cmd, strings.Join(c.args, " "), err)
		}
	}
}

// #171: `memory add --kind fact "..."` stored "--kind fact ..." as the fact.
func TestMemoryAddRefusesAFlagRatherThanStoringItInsideTheFact(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_RUNTIME", "")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })

	err := memoryCmd([]string{"add", "--kind", "fact", "staging runs on port 8080"})
	if err == nil || !strings.Contains(err.Error(), `unknown flag "--kind"`) {
		t.Fatalf("memory add --kind should be refused by name, got %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(vaultDir, "memories", "fact.md")); strings.Contains(string(raw), "--kind") {
		t.Errorf("the flag was written into the vault:\n%s", raw)
	}
}

// #166: `memory add "x" --project ../etc` recorded a project literally named
// "../etc". The CLI has to reduce a path-shaped name the way the MCP server does.
func TestMemoryAddReducesAPathShapedProjectToItsName(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_RUNTIME", "")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })
	// Standing outside any repository, so "../etc" points somewhere that is
	// named etc — from inside this one it would rightly be filed under it.
	t.Chdir(t.TempDir())

	out := captureStdout(t, func() {
		if err := memoryCmd([]string{"add", "the build runs on arm", "--project", "../etc"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "../etc") {
		t.Errorf("the memory was scoped to the literal path:\n%s", out)
	}
	if !strings.Contains(out, "remembered in etc") {
		t.Errorf("expected the memory scoped to etc:\n%s", out)
	}
}
