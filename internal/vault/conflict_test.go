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
