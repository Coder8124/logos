package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A checkpoint or plan name is claimed with an empty O_EXCL create and filled
// by a later atomic write. A kill between the two left a zero-byte checkpoint
// that every index walked past for good (#380). Index removes one that has
// sat empty longer than any stalled write, and says so; a fresh empty one may
// be a claim whose write is still coming, and a person's empty note is not a
// claim at all.
func TestIndexRemovesACheckpointLeftEmptyByAKilledWriteAndSaysSo(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_EMBED", "off")
	old := time.Now().Add(-2 * time.Hour)
	put := func(rel, body string, aged bool) string {
		p := filepath.Join(vault, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if aged {
			os.Chtimes(p, old, old)
		}
		return p
	}
	killedCheckpoint := put("sessions/kestrel/_convos/_unattached/20261102-232711-chaos.md", "", true)
	killedPlan := put("sessions/kestrel/plans/plan-20261102-232711-chaos.md", "", true)
	liveClaim := put("sessions/kestrel/_convos/_unattached/20261105-101500-chaos.md", "", false)
	written := put("sessions/kestrel/_convos/_unattached/20261102-101500-chaos.md", "---\ntype: checkpoint\n---\nwork\n", true)
	personsNote := put("notes/20261102-empty idea.md", "", true)

	out := captureStdout(t, func() {
		if err := runIndex(false); err != nil {
			t.Fatalf("index: %v", err)
		}
	})
	for _, p := range []string{killedCheckpoint, killedPlan} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left empty by a killed write is still there", p)
		}
	}
	for _, p := range []string{liveClaim, written, personsNote} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if !strings.Contains(out, "removed 2 empty checkpoints left by an interrupted write") {
		t.Errorf("index did not say it removed the two empty claims:\n%s", out)
	}
}
