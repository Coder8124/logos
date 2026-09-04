package vault

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// childVaultEnv marks the subprocess run below. Its presence is what tells the
// helper it is the child rather than an ordinary `go test` invocation.
const childVaultEnv = "BRAIN_LOCK_TEST_VAULT"

// The whole reason this lock exists is the case a mutex cannot reach: Claude
// Code and Cursor each run their own `brain mcp serve`, in their own process,
// against one vault. Asserting that from inside a single process would prove
// nothing — a sync.Mutex passes that test — so this spawns a real second
// process and measures whether it had to wait.
//
// The old code had no cross-process guard at all while its comment claimed the
// two-agent case as the one it protected. This is the test that would have
// caught the difference.
func TestALockIsHeldAgainstAnotherProcess(t *testing.T) {
	if os.Getenv(childVaultEnv) != "" {
		t.Skip("this run is the child; see TestHelperWaitsForTheLock")
	}
	dir := t.TempDir()

	const held = 700 * time.Millisecond

	g, err := Lock(dir, "probe")
	if err != nil {
		t.Fatal(err)
	}
	if !g.locked {
		t.Skipf("the OS lock is unavailable on this filesystem, so there is nothing to prove here")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(held)
		g.Unlock()
	}()

	// The child asks for the same lock and reports how long it waited.
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperWaitsForTheLock", "-test.v")
	cmd.Env = append(os.Environ(), childVaultEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	wg.Wait()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}

	waited, ok := parseWaited(string(out))
	if !ok {
		t.Fatalf("child did not report how long it waited:\n%s", out)
	}
	// Generous margin: the assertion is "it waited for us", not a timing
	// measurement. A second process that is not excluded returns in microseconds.
	if waited < held/2 {
		t.Fatalf("the second process took the lock after %s while this one held it for %s — "+
			"two agents against one vault are not serialised", waited, held)
	}
}

// TestHelperWaitsForTheLock is the child half of the test above, not a test of
// its own — it skips unless the parent put the vault in the environment.
func TestHelperWaitsForTheLock(t *testing.T) {
	dir := os.Getenv(childVaultEnv)
	if dir == "" {
		t.Skip("run only as a subprocess of TestALockIsHeldAgainstAnotherProcess")
	}
	start := time.Now()
	g, err := Lock(dir, "probe")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("waited-ns=%d", time.Since(start).Nanoseconds())
	g.Unlock()
}

func parseWaited(out string) (time.Duration, bool) {
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "waited-ns=")
		if i < 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(line[i+len("waited-ns="):]), 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(n), true
	}
	return 0, false
}

// Two goroutines in one process are the other half. flock is held per open file
// description, so each goroutine opening its own descriptor can be granted the
// "same" exclusive lock — the in-process mutex is what covers this, and neither
// half is sufficient alone.
func TestALockIsHeldAgainstAnotherGoroutine(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	inside := 0
	worst := 0

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := Lock(dir, "probe")
			if err != nil {
				t.Error(err)
				return
			}
			defer g.Unlock()
			mu.Lock()
			inside++
			if inside > worst {
				worst = inside
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
		}()
	}
	wg.Wait()

	if worst > 1 {
		t.Fatalf("%d goroutines held the same lock at once", worst)
	}
}

// The lock lives in .brain, which is the half of the vault the product tells
// people they can delete. A lock file written anywhere else would be durable
// state that survives `rm -rf .brain` and means nothing after a reboot.
func TestLockFilesLiveInTheDisposableHalfOfTheVault(t *testing.T) {
	dir := t.TempDir()

	g, err := Lock(dir, "probe")
	if err != nil {
		t.Fatal(err)
	}
	g.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".brain" {
			t.Errorf("locking put %q in the vault; only .brain should have been touched", e.Name())
		}
	}
	if _, err := os.Stat(dir + "/.brain/locks/probe.lock"); err != nil {
		t.Errorf("expected the lock file under .brain/locks: %v", err)
	}
}
