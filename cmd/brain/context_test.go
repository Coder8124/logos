package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Coder8124/brain/internal/contextpack"
)

// brain context --pin is the CLI's half of the tree view's control surface —
// the desktop app and the command line write the same file, so a rule set
// from a terminal must be exactly as durable as one set by clicking a node.
func TestBrainContextPinWritesADurablePathRule(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runContext([]string{"--pin", "sessions/old-project"}); err != nil {
		t.Fatalf("--pin returned an error: %v", err)
	}

	rules, err := contextpack.LoadPathRules(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Prefix != "sessions/old-project" || rules[0].Pin != contextpack.PathPinAlways {
		t.Fatalf("expected one pin rule on sessions/old-project, got %v", rules)
	}
}

func TestBrainContextExcludeThenUnpinClearsTheRule(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runContext([]string{"--exclude", "memories/context.md"}); err != nil {
		t.Fatalf("--exclude returned an error: %v", err)
	}
	rules, err := contextpack.LoadPathRules(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Pin != contextpack.PathPinNever {
		t.Fatalf("expected one exclude rule, got %v", rules)
	}

	if err := runContext([]string{"--unpin", "memories/context.md"}); err != nil {
		t.Fatalf("--unpin returned an error: %v", err)
	}
	rules, err = contextpack.LoadPathRules(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected the rule to be cleared, got %v", rules)
	}
}

// --rules with no vault must fail the same way every other vault-reading verb
// does — the existing missingVaultError, not a bespoke message that a script
// parsing stderr would have to special-case.
func TestBrainContextRulesWithNoVaultReportsTheStandardMissingVaultError(t *testing.T) {
	vault := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("BRAIN_VAULT", vault)

	err := runContext([]string{"--rules"})
	if err == nil {
		t.Fatal("expected an error for a vault that does not exist")
	}
	want := missingVaultError(vault).Error()
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}
