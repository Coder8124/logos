package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A damaged digest elsewhere makes the index refuse, and the refusal returned
// before the notice: the unreadable file the same scan found went unnamed, so
// the one run that had found it said nothing about it.
func TestAnUnreadableMemoryFileIsNamedWhenTheIndexRefuses(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file permission that stops its owner reading it")
	}
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	if err := memoryCmd([]string{"add", "--project", "shop", "Deploys go out on Tuesdays"}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(vaultDir, "memories", "fact", "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("got %v per-memory fact files (%v), want 1", matches, err)
	}
	locked := matches[0]
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })
	torn := filepath.Join(vaultDir, "memories", "context.md")
	if err := os.WriteFile(torn, []byte("- Shipping in November <!-- uid=0123"), 0o600); err != nil {
		t.Fatal(err)
	}

	var ierr error
	out := captureStdout(t, func() { ierr = runIndex(false) })
	if ierr == nil {
		t.Fatal("index accepted a torn context.md")
	}
	if want := "memories/fact/" + filepath.Base(locked); !strings.Contains(out, want) {
		t.Errorf("the refusing index did not name the file it could not read; want %q in:\n%s", want, out)
	}
}
