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
	want := "could not read 1 memory file; a memory the index already has is kept, but none is read from it until it is readable: memories/fact/" + filepath.Base(locked) + ": permission denied"
	if !strings.Contains(out, want) {
		t.Errorf("index did not name the file it could not read; want %q in:\n%s", want, out)
	}
}

// Over an index that already had the memory, an unreadable file read as a line
// the user deleted: the index forgot the memory and removed the file it could
// not read, so fixing the permission afterwards brought nothing back.
func TestAnUnreadableMemoryFileIsNotForgottenByTheNextIndex(t *testing.T) {
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

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "forgot") {
		t.Errorf("index forgot the memory whose file it could not read:\n%s", out)
	}
	if _, err := os.Lstat(locked); err != nil {
		t.Fatalf("index removed the memory file it could not read: %v", err)
	}
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatal(err)
	}
	listed := captureStdout(t, func() {
		if err := memoryCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, text := range []string{"Stripe", "Tuesdays"} {
		if !strings.Contains(listed, text) {
			t.Errorf("%q is not listed after the file was readable again:\n%s", text, listed)
		}
	}
}
