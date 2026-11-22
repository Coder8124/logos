package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/index"
)

// A checkpoint written after the last index was invisible to search, and the
// answer was the same "Nothing in the vault matches" as for a word that was
// never written: the user cannot tell a miss from a stale cache.
func TestSearchSaysWhenTheVaultHasNotesTheIndexHasNotSeen(t *testing.T) {
	vaultDir := standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	ix, err := index.Open(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	note := filepath.Join(vaultDir, "late.md")
	if err := os.WriteFile(note, []byte("---\ntype: note\n---\nzebrafish migration plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(note, later, later); err != nil {
		t.Fatal(err)
	}

	var out string
	errOut := captureStderr(t, func() {
		out = captureStdout(t, func() {
			if err := search("zebrafish"); err != nil {
				t.Fatal(err)
			}
		})
	})

	if !strings.Contains(out+errOut, "logos index") || !strings.Contains(out+errOut, "newer") {
		t.Errorf("search gave no sign the index is behind the vault\nstdout: %s\nstderr: %s", out, errOut)
	}
}
