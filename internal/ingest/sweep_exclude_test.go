package ingest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/exclude"
)

// A transcript the pre-read look could not place (the seam hands the sweep
// sessions already parsed) is still held at the chokepoint, and counted, so an
// excluded chat cannot become a checkpoint by arriving a different way.
func TestASweepDoesNotRecordAnExcludedSessionAndCountsIt(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exclude.AddProject(vault, dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	s.Cwd = dir
	onMachine(t, s)

	wrote, grew, problems, skips := SweepCounted(vault, "shop", now)
	if len(wrote)+len(grew) != 0 || len(problems) != 0 {
		t.Fatalf("an excluded session was recorded: wrote %d grew %d problems %v", len(wrote), len(grew), problems)
	}
	if got := skips.Line(); got != "1 session skipped (excluded project)" {
		t.Errorf("receipt %q", got)
	}
	if _, err := os.Stat(vault + "/sessions"); err == nil {
		t.Error("the sweep left a sessions folder for an excluded chat")
	}
}
