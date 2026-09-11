package rename

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A checkout that renamed itself after already accumulating history (a
// .logos-project marker written after the fact) ends up with two project
// names for one piece of work: the old sessions/<old>/ directory and the new
// sessions/<new>/ one, both real, neither superseding the other. Plain rename
// refuses this outright (TestRenameRefusesToMergeIntoAnExistingProject) —
// correctly, for an ordinary rename, but it leaves no way to heal a split
// history at all. --merge is that way, and it must fail before it exists.
func TestMergeCombinesCheckpointsFromBothProjects(t *testing.T) {
	v := seedSplitVault(t)
	db := seedDB(t)

	res, err := Run(db, v, "logos", "brain", false, true)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(v, "sessions", "logos")); !os.IsNotExist(err) {
		t.Error("the source session directory still exists after a merge")
	}

	// Both the pre-existing brain checkpoint and the migrated logos one must
	// be present — a merge that preserves one set at the expense of the other
	// is exactly the silent history split this exists to heal.
	if got := read(t, filepath.Join(v, "sessions", "brain", "20260101-000000-claude.md")); !strings.Contains(got, "project: brain") {
		t.Errorf("the pre-existing brain checkpoint was disturbed:\n%s", got)
	}
	got := read(t, filepath.Join(v, "sessions", "brain", "20260201-000000-claude.md"))
	if !strings.Contains(got, "project: brain") {
		t.Errorf("the migrated checkpoint's frontmatter was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "history from the logos name") {
		t.Errorf("the migrated checkpoint's prose was lost:\n%s", got)
	}

	if res.Merged != 1 {
		t.Errorf("Merged = %d, want 1 (one file moved from logos into brain)", res.Merged)
	}
	if res.Collisions != 0 {
		t.Errorf("Collisions = %d, want 0 — these two files did not share a name", res.Collisions)
	}
}

// Two checkpoints filed under the same second by the same agent — one from
// each project's history — collide on filename the moment they land in one
// directory. Silently letting the second os.Rename overwrite the first would
// be the exact failure this command exists to prevent: history quietly
// dropped during a "successful" merge.
func TestMergeKeepsBothCheckpointsOnAFilenameCollision(t *testing.T) {
	v := seedSplitVault(t)
	// Force a same-name collision: the logos side gets a checkpoint with the
	// identical filename as the pre-existing brain one.
	dup := filepath.Join(v, "sessions", "logos", "20260101-000000-claude.md")
	if err := os.WriteFile(dup, []byte("---\ntype: checkpoint\nproject: logos\n---\n\nthe colliding one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(nil, v, "logos", "brain", false, true)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if res.Collisions != 1 {
		t.Fatalf("Collisions = %d, want 1", res.Collisions)
	}

	entries, err := os.ReadDir(filepath.Join(v, "sessions", "brain"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	// The original survives under its name; the incoming duplicate survives
	// under a disambiguated one. Neither vanished.
	if !names["20260101-000000-claude.md"] {
		t.Error("the original brain checkpoint is gone")
	}
	found := false
	for n := range names {
		if n != "20260101-000000-claude.md" && n != "20260201-000000-claude.md" && strings.HasSuffix(n, ".md") {
			found = true
		}
	}
	if !found {
		t.Errorf("the colliding checkpoint has no surviving disambiguated file; entries: %v", names)
	}
}

// --dry-run has to be trustworthy for a merge exactly as it is for a plain
// rename: it reports what it would do and changes nothing on disk.
func TestMergeDryRunTouchesNothing(t *testing.T) {
	v := seedSplitVault(t)

	res, err := Run(nil, v, "logos", "brain", true, true)
	if err != nil {
		t.Fatalf("dry-run merge failed: %v", err)
	}
	if res.Merged == 0 {
		t.Error("a dry-run merge reported nothing to move")
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "logos")); err != nil {
		t.Error("a dry-run merge moved the source directory")
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "logos", "20260201-000000-claude.md")); err != nil {
		t.Error("a dry-run merge removed the file it would have moved")
	}
}

// Without --merge, landing on an existing project must still refuse — the
// new flag must not loosen the default, only add an explicit opt-in.
func TestMergeIsNotImpliedByDefault(t *testing.T) {
	v := seedSplitVault(t)
	if _, err := Run(nil, v, "logos", "brain", false, false); err == nil {
		t.Fatal("renaming onto an existing project without --merge should still refuse")
	}
	if _, err := os.Stat(filepath.Join(v, "sessions", "logos")); err != nil {
		t.Error("the refused rename moved the directory anyway")
	}
}

// seedSplitVault builds the shape of the real bug: sessions/brain already has
// one checkpoint (as if from a long-lived project), and sessions/logos has
// one more (as if the checkout renamed itself after the fact and everything
// since has been filed under the new name) — two directories for one thread
// of work.
func seedSplitVault(t *testing.T) string {
	t.Helper()
	v := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(v, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("sessions/brain/20260101-000000-claude.md", `---
type: checkpoint
project: brain
agent: claude
---

## Task

the original brain checkpoint
`)
	mk("sessions/logos/20260201-000000-claude.md", `---
type: checkpoint
project: logos
agent: claude
---

## Task

history from the logos name, after the checkout renamed itself
`)
	return v
}
