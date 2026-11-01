package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A memory file the index cannot read was skipped with a bare continue: after
// a rebuild `logos index` counted one memory fewer, the digest was rewritten
// without it, and nothing named the file still sitting on disk.
func TestAnUnreadableMemoryFileIsNamedByTheIndexThatLeftItOut(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file permission that stops its owner reading it")
	}
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	for _, text := range []string{"Decided to use Stripe for payments", "Deploys go out on Tuesdays"} {
		if err := memoryCmd([]string{"add", "--project", "shop", text}); err != nil {
			t.Fatalf("memory add: %v", err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(vaultDir, "memories", "fact", "*.md"))
	if err != nil || len(matches) != 2 {
		t.Fatalf("got %v per-memory fact files (%v), want 2", matches, err)
	}
	locked := matches[0]
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })
	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	want := "could not read 1 memory file, left out of memory until it is readable: memories/fact/" + filepath.Base(locked) + ": permission denied"
	if !strings.Contains(out, want) {
		t.Errorf("index did not name the file it could not read; want %q in:\n%s", want, out)
	}
}
