package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
)

// The note slug folds "_" into "-", which is right as a key and wrong as
// something to copy: search printed sessions/p/-convos/-unattached/…, a path
// that does not exist, while resume and why print the real one.
func TestSearchPrintsThePathAUserCanOpenNotTheSlug(t *testing.T) {
	vaultDir := standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	dir := filepath.Join(vaultDir, "sessions", "shop", "_convos", "_unattached")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20261116-cli.md"), []byte("---\ntype: checkpoint\n---\npratt parsing decision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := search("pratt"); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "sessions/shop/_convos/_unattached/20261116-cli") || strings.Contains(out, "-convos") {
		t.Errorf("search printed a path that is not on disk:\n%s", out)
	}
}
