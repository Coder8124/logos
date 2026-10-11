package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/auto"
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

// With the index switch on, the deleted index is rebuilt before the command
// answers, said on stderr, logged for the next resume, and rebuilt once.
func TestTheIndexSwitchRebuildsADeletedIndexBeforeTheCommandAnswers(t *testing.T) {
	standIn(t, "shop")
	if err := os.WriteFile(filepath.Join(vaultPath(), "cache.md"), []byte("# Cache\n\nThe cart cache expires.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_EMBED", "off")
	if err := auto.Set(vaultPath(), "index", true); err != nil {
		t.Fatal(err)
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
		if !strings.Contains(out, "cache") {
			t.Errorf("search %d after the rebuild did not find the note:\nstdout: %s\nstderr: %s", i+1, out, errOut)
		}
		rebuilt := strings.Contains(errOut, "rebuilding")
		if rebuilt != (i == 0) {
			t.Errorf("search %d: rebuilt=%v, want only the first to rebuild:\n%s", i+1, rebuilt, errOut)
		}
		if strings.Contains(out, "restored") || strings.Contains(out, "lexical only") {
			t.Errorf("the rebuild's report went to stdout, which is the search's answer:\n%s", out)
		}
	}
	entries, err := auto.Entries(vaultPath(), "", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := auto.Summary(entries); !strings.Contains(got, "rebuilt the missing index") || strings.Contains(got, "times") {
		t.Errorf("the next resume will not say the index was rebuilt once: %q", got)
	}
}

// The demo builds its own scratch vault, which has never been indexed; telling
// the room that vault has no index, right after saying their real one is
// untouched, is a false alarm in the one place it is read aloud.
func TestTheDemoDoesNotWarnThatItsScratchVaultHasNoIndex(t *testing.T) {
	standIn(t, "shop")
	t.Setenv("LOGOS_EMBED", "off")
	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			if err := runDemo([]string{"--fast", "--vault", t.TempDir()}); err != nil {
				t.Fatal(err)
			}
		})
	})
	if strings.Contains(errOut, "no index") {
		t.Errorf("the demo warned about its own scratch index:\n%s", errOut)
	}
}
