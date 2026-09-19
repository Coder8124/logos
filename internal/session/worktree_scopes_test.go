package session

import (
	"os"
	"path/filepath"
	"testing"
)

func seedCheckpoint(t *testing.T, vault, scope string) {
	t.Helper()
	dir := filepath.Join(vault, CheckpointDir, filepath.FromSlash(scope))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: checkpoint\nproject: " + scope + "\nagent: claude\n---\n\n## Task\n\nfix the checkout crash\n\n## Next\n\nship it\n"
	if err := os.WriteFile(filepath.Join(dir, "20260919-120000-claude.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// safeScope preserves "/" on purpose, so a checkpoint written from a linked
// git worktree — including the worktrees Claude Code creates for its own
// agents — lands in sessions/<project>/<worktree>/. Projects is one ReadDir
// and cannot see it, so every enumerator built on it reported a number as
// though it were complete: `logos projects` undercounted, list_projects named
// a parent with "(0 checkpoints)", and internal/deadend never reached a ruling
// recorded from a worktree, which is the one thing that package is for.
func TestScopesFindsACheckpointWrittenFromAWorktree(t *testing.T) {
	vault := t.TempDir()
	seedCheckpoint(t, vault, "shop/fix-auth")
	seedCheckpoint(t, vault, "shop/perf")

	got, err := Scopes(vault)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"shop/fix-auth": true, "shop/perf": true}
	for _, g := range got {
		delete(want, g)
	}
	if len(want) > 0 {
		t.Errorf("worktree scopes are invisible to enumeration: missing %v, got %v", want, got)
	}
}

// The scope a caller gets back has to be the one it can hand to History, or
// the name is decoration. This is what made list_projects offer "shop" for a
// vault whose only checkpoint was under shop/fix-auth.
func TestAScopeFromScopesReadsBackItsOwnHistory(t *testing.T) {
	vault := t.TempDir()
	seedCheckpoint(t, vault, "shop/fix-auth")

	scopes, err := Scopes(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 {
		t.Fatalf("want the one scope holding the checkpoint, got %v", scopes)
	}
	h, err := History(vault, scopes[0], 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) == 0 {
		t.Errorf("the scope %q was listed but its history reads back empty", scopes[0])
	}
}

// A flat project and a worktree under the same project are two scopes, and a
// checkpoint belongs to exactly one of them. Counting both levels naively
// would double a vault that has some of each.
func TestAProjectAndItsWorktreeAreCountedAsSeparateScopes(t *testing.T) {
	vault := t.TempDir()
	seedCheckpoint(t, vault, "shop")
	seedCheckpoint(t, vault, "shop/fix-auth")

	got, err := Scopes(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want shop and shop/fix-auth, got %v", got)
	}
	total := 0
	for _, s := range got {
		h, _ := History(vault, s, 0)
		total += len(h)
	}
	if total != 2 {
		t.Errorf("the two checkpoints counted %d across the scopes", total)
	}
}

// A directory with no session record in it is not a scope. `logos projects`
// inventing a parent with "(0 checkpoints)" and contradicting itself in one
// sentence is the shape this prevents.
func TestADirectoryHoldingNoRecordIsNotAScope(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, CheckpointDir, "empty-project"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedCheckpoint(t, vault, "shop")

	got, err := Scopes(vault)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g == "empty-project" {
			t.Errorf("a directory holding nothing was listed as a scope with work in it: %v", got)
		}
	}
}

// `logos tried --ruled-out` records a dead end as a working note, so it can be
// recorded an hour into a session rather than only at its end. A scope with
// notes and no checkpoint yet is that session still in progress, and dropping
// it would make internal/deadend blind in its freshest case.
func TestAScopeWithOnlyWorkingNotesIsStillFound(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, CheckpointDir, "kestrel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, NotesFile), []byte("# uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Scopes(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "kestrel" {
		t.Errorf("a scope whose only record is working notes was dropped: %v", got)
	}
}
