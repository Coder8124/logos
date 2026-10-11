package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deleteIndex(t *testing.T) {
	t.Helper()
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		if err := os.Remove(filepath.Join(vaultPath(), ".logos", f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestRecallOnADeletedIndexIsNotASQLError(t *testing.T) {
	standIn(t, "shop")
	deleteIndex(t)
	captureStdout(t, func() {
		if err := recallCmd([]string{"postgres"}); err != nil {
			t.Errorf("recall on a deleted index failed: %v", err)
		}
	})
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()
	fn()
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// "Nothing in the vault matches" from an index nobody rebuilt is a confident
// wrong answer; the command has to say the index is what is empty.
func TestSearchOnADeletedIndexSaysTheIndexIsMissing(t *testing.T) {
	standIn(t, "shop")
	if err := os.WriteFile(filepath.Join(vaultPath(), "cache.md"), []byte("# Cache\n\nThe cart cache expires.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_EMBED", "off")
	captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	// And only then: a notice on every search of a built index is one nobody
	// reads by the time it is true.
	if errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			if err := search("cart"); err != nil {
				t.Fatal(err)
			}
		})
	}); strings.Contains(errOut, "no index") {
		t.Errorf("search on a built index said it had none: %s", errOut)
	}
	deleteIndex(t)
	for i := 0; i < 2; i++ {
		var out string
		errOut := captureStderr(t, func() {
			out = captureStdout(t, func() {
				if err := search("cart"); err != nil {
					t.Fatal(err)
				}
			})
		})
		if !strings.Contains(errOut, "no index") || !strings.Contains(errOut, "logos index") {
			t.Errorf("search %d on a deleted index did not say so:\nstdout: %s\nstderr: %s", i+1, out, errOut)
		}
	}
}
