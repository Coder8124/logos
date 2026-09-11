package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A vault that lives inside a git repo — the whole point of "a vault two
// people can share over git" — must never let .brain/ (the disposable
// SQLite cache, rebuilt from markdown by `brain index`) go into version
// control. Committing it defeats the plan: two clones would fight over a
// binary file that carries no information the markdown doesn't already
// have, on every pull.
func TestEnsureGitignoreAddsBrainDirToAFreshVault(t *testing.T) {
	dir := t.TempDir()

	wrote, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	if !wrote {
		t.Error("a vault with no .gitignore yet should report that it wrote one")
	}

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(got), ".brain/") {
		t.Errorf(".gitignore does not exclude .brain/:\n%s", got)
	}
}

// A second call — every `brain index` run, not just the first one ever — must
// not grow the file. Appending a duplicate line every run would make the
// .gitignore itself into churn the vault's own git history has to carry.
func TestEnsureGitignoreIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := EnsureGitignore(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}

	wrote, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if wrote {
		t.Error("a second call reported writing again, but .brain/ was already ignored")
	}

	after, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf(".gitignore changed on a call that should have been a no-op:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A user who already has a .gitignore for other reasons — their own editor
// junk, a build directory that happens to live beside the vault — must keep
// every line they wrote. This only ever appends.
func TestEnsureGitignorePreservesExistingLines(t *testing.T) {
	dir := t.TempDir()
	existing := "*.swp\nnode_modules/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	if !wrote {
		t.Error("adding a new line to an existing .gitignore should report that it wrote")
	}

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "*.swp") || !strings.Contains(s, "node_modules/") {
		t.Errorf("existing lines were lost:\n%s", s)
	}
	if !strings.Contains(s, ".brain/") {
		t.Errorf(".brain/ was not added:\n%s", s)
	}
}

// A .gitignore that already excludes .brain via some other pattern — the
// user wrote it themselves, or a future version of this function used a
// slightly different line — must not be treated as absent. Matching on
// substring rather than exact line equality means a line like "/.brain/"
// or ".brain" (no trailing slash) still counts as already covering it.
func TestEnsureGitignoreRecognizesAnExistingBrainRuleWrittenByHand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# my own rules\n.brain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := EnsureGitignore(dir)
	if err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	if wrote {
		t.Error("a hand-written .brain rule should already satisfy this, without another write")
	}
}
