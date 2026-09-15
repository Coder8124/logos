package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Windows the running binary is moved aside before the new one is renamed
// in. When that second rename failed (antivirus holding the file, say), swap
// returned with the old binary still at .old and nothing at the install path,
// so the next `logos` the user typed was not found.
func TestAFailedWindowsSwapPutsTheOldBinaryBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "logos.exe")
	if err := os.WriteFile(target, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "logos.exe.new") // never written, so the rename in fails

	err := swapOn("windows", missing, target)
	if err == nil {
		t.Fatal("swap reported success installing a binary that does not exist")
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil || string(got) != "old build" {
		t.Fatalf("the install path lost its binary after a failed swap: %v (%q)", rerr, got)
	}
	if !strings.Contains(err.Error(), "old one is back in place") {
		t.Errorf("the error should say the old binary was restored, got %q", err)
	}
}
