//go:build unix

package session

import (
	"fmt"
	"os"
	"os/exec"
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
	// The limit is per process, so it is set in a child: lowered here it also
	// caught the test binary's own log once the package wrote past 64KB of it,
	// and every test running alongside.
	if dir := os.Getenv("LOGOS_TEST_FSIZE_CHILD"); dir != "" {
		saveUnderFileSizeLimit(dir)
		return
	}
	vaultDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAPlanWriteCutShortLeavesNoHalfPlan$")
	cmd.Env = append(os.Environ(), "LOGOS_TEST_FSIZE_CHILD="+vaultDir)
	out, err := cmd.CombinedOutput()
	switch {
	case strings.Contains(string(out), "SKIP:"):
		t.Skip(strings.TrimSpace(string(out)))
	case strings.Contains(string(out), "SAVED"):
		t.Fatal("a plan past the file-size limit was saved without an error")
	case err != nil || !strings.Contains(string(out), "REFUSED"):
		t.Fatalf("the limited save did not run: %v\n%s", err, out)
	}

	entries, _ := os.ReadDir(filepath.Join(vaultDir, CheckpointDir, "kestrel", PlanDir))
	for _, e := range entries {
		t.Errorf("a failed save left %s behind", e.Name())
	}
	if plans, _ := ListPlans(vaultDir, "kestrel"); len(plans) != 0 {
		t.Errorf("a failed save is listed as %d plan(s)", len(plans))
	}
}

// saveUnderFileSizeLimit is the child's half: one plan save under a 64KB
// file-size limit, as a full disk stops a write, reported on stdout.
func saveUnderFileSizeLimit(vaultDir string) {
	signal.Ignore(syscall.SIGXFSZ) // so an over-limit write fails instead of killing the process
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		fmt.Println("SKIP: no file-size limit here:", err)
		return
	}
	limit.Cur = 64 << 10
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		fmt.Println("SKIP: cannot set a file-size limit:", err)
		return
	}
	p := Plan{Project: "kestrel", Agent: "claude", Text: strings.Repeat("step of a long plan\n", 1<<16), TS: 1755172800}
	if _, err := SavePlan(vaultDir, p); err != nil {
		fmt.Println("REFUSED")
		return
	}
	fmt.Println("SAVED")
}
