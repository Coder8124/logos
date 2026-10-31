package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// The names iCloud, Dropbox and Syncthing give the losing side of a conflict
// are set aside only beside their original: a lone "x 2.md" is the only copy
// of whatever it holds, and a name merely ending in a number is not a copy.
func TestASyncConflictCopyIsRecognisedOnlyBesideItsOriginal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20261028-101500-claude.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"20261028-101500-claude 2.md":                                   true,
		"20261028-101500-claude 12.md":                                  true,
		"20261028-101500-claude (conflicted copy).md":                   true,
		"20261028-101500-claude (Sam's conflicted copy 2026-10-28).md":  true,
		"20261028-101500-claude.sync-conflict-20261028-101500-ABC12.md": true,
		"20261028-101500-claude.md":                                     false, // the original itself
		"20261028-101500-claude 1.md":                                   false, // iCloud starts at 2
		"20261028-101500-claude 0.md":                                   false,
		"20261028-101500-other 2.md":                                    false, // its original is not here
		"20261028-101500-claude2.md":                                    false,
	} {
		if got := IsConflictCopy(dir, name); got != want {
			t.Errorf("IsConflictCopy(%q) = %v, want %v", name, got, want)
		}
	}
}

// A copy of a copy names the file it was copied from, which may itself be a
// copy that is gone, so it is traced back to the root original. Google Drive
// and OneDrive number from 1 in parentheses.
func TestACopyOfACopyAndADriveCopyAreTracedBackToTheirOriginal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20261028-101500-claude.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"20261028-101500-claude 2 (conflicted copy).md":                   true,
		"20261028-101500-claude (1).md":                                   true,
		"20261028-101500-claude (12).md":                                  true,
		"20261028-101500-claude 2.sync-conflict-20261028-101500-ABC12.md": true,
		"20261028-101500-claude (conflicted copy) 2.md":                   true,
		"20261028-101500-claude (0).md":                                   false,
		"20261028-101500-other (1).md":                                    false,
	} {
		if got := IsConflictCopy(dir, name); got != want {
			t.Errorf("IsConflictCopy(%q) = %v, want %v", name, got, want)
		}
	}
	if got := ConflictOriginal("abc 2 (Sam's conflicted copy).md"); got != "abc.md" {
		t.Errorf("ConflictOriginal of a copy of a copy = %q, want abc.md", got)
	}
}

func TestCopiesOfFindsEveryCopyOfOneFileAndNoOther(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.md", "a 2.md", "a 2 (conflicted copy).md", "a (1).md", "b 2.md", "a2.md"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	got, err := CopiesOf(dir, "a.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a 2.md": true, "a 2 (conflicted copy).md": true, "a (1).md": true}
	if len(got) != len(want) {
		t.Errorf("CopiesOf = %v, want the three copies of a.md", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("CopiesOf returned %q, which is not a copy of a.md", g)
		}
	}
}
