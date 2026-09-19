package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/vault"
)

// #125: `logos index` wrote the .gitignore and setup did not, so the vault
// every user gets on day one offered .logos/index.db and the activity log to
// the first `git add -A`.
func TestAVaultBuiltBySetupKeepsTheIndexAndActivityLogOutOfGit(t *testing.T) {
	saved := provider.LocalEndpoints
	provider.LocalEndpoints = nil // no model runtime: this is about files, not embeddings
	t.Cleanup(func() { provider.LocalEndpoints = saved })

	dir := t.TempDir()
	if err := indexVault(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("setup built an index and left no .gitignore: %v", err)
	}
	for _, rule := range []string{".logos/", "activity/"} {
		if !strings.Contains(string(got), rule) {
			t.Errorf(".gitignore = %q, missing %q", got, rule)
		}
	}
}

// #126: an index that could not be built was printed and forgotten, and setup
// went on to wire every host to the path.
func TestAnIndexSetupCouldNotBuildIsAnErrorItsCallerSees(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notavault")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := indexVault(file); err == nil {
		t.Error("indexVault on a regular file returned nil; setup would carry on and wire hosts to it")
	}
}

// #126: a --vault naming a file was recorded machine-wide before anything had
// tried to use it, so the pointer outlived the failed run.
func TestSetupRefusesAVaultThatIsAFileAndRecordsNothing(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	before := vault.Recorded()

	file := filepath.Join(t.TempDir(), "notavault")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{false, true} {
		_, _, _, err := chooseVault([]string{"--vault", file, "--yes", "--record-temp"}, dryRun)
		if err == nil {
			t.Fatalf("dryRun=%v: chooseVault accepted a regular file as the vault", dryRun)
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("dryRun=%v: error %q does not say what is wrong with the path", dryRun, err)
		}
	}
	if now := vault.Recorded(); now != before {
		t.Errorf("a refused vault was recorded anyway: pointer moved from %q to %q", before, now)
	}
}
