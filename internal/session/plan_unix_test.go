//go:build unix

package session

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A plan was filled with a plain write over its claimed name, so a write cut
// short — a kill, a full disk — left half a plan under a real plan's name,
// listed and indexed as one (#384). A file-size limit stops the write part way,
// as a full disk does, and nothing torn may be left behind.
func TestAPlanWriteCutShortLeavesNoHalfPlan(t *testing.T) {
	vaultDir := t.TempDir()
	p := Plan{Project: "kestrel", Agent: "claude", Text: strings.Repeat("step of a long plan\n", 1<<16), TS: 1755172800}

	signal.Ignore(syscall.SIGXFSZ) // so an over-limit write fails instead of killing the test
	defer signal.Reset(syscall.SIGXFSZ)
	var was syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &was); err != nil {
		t.Skipf("no file-size limit here: %v", err)
	}
	limit := was
	limit.Cur = 64 << 10
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Skipf("cannot set a file-size limit: %v", err)
	}
	_, err := SavePlan(vaultDir, p)
	syscall.Setrlimit(syscall.RLIMIT_FSIZE, &was)
	if err == nil {
		t.Fatal("a plan past the file-size limit was saved without an error")
	}

	entries, _ := os.ReadDir(filepath.Join(vaultDir, CheckpointDir, "kestrel", PlanDir))
	for _, e := range entries {
		t.Errorf("a failed save left %s behind", e.Name())
	}
	if plans, _ := ListPlans(vaultDir, "kestrel"); len(plans) != 0 {
		t.Errorf("a failed save is listed as %d plan(s)", len(plans))
	}
}
