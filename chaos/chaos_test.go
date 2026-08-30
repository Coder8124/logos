//go:build chaos

// Package chaos runs the real brain binary against a real vault and then breaks
// the machine underneath it.
//
// Everything else in the test suite runs in-process, where a "crash" is a
// simulation and a "full disk" is a mock. These tests use SIGKILL, a mounted
// disk image filled to capacity, and two processes racing on one vault, because
// the failures worth finding are the ones that only appear when the kernel is
// involved: a rename that never reached the platter, a WAL left mid-transaction,
// a write that returned ENOSPC halfway through.
//
//	go test -tags chaos ./chaos -v
//
// Requires: a built binary (the suite builds one), macOS or Linux, and nothing
// else. No vault of the user's is ever touched — everything happens under
// t.TempDir() or a scratch disk image.
package chaos

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// bin builds the CLI once and returns its path.
func bin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "brain-chaos-bin")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "brain")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/brain")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("building the CLI: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

func run(t *testing.T, vault string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin(t), args...)
	cmd.Env = append(os.Environ(), "BRAIN_VAULT="+vault, "BRAIN_AGENT=chaos")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func newVault(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vault")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A checkpoint is either fully there or not there. A half-written one is the
// failure that matters, because resume reads it and hands an agent a lie.
//
// vault.WriteAtomic writes to a temp file, fsyncs it, and renames. It does not
// fsync the parent directory afterwards, so on a power cut the rename itself can
// be lost even though the data reached the disk. SIGKILL cannot reproduce that
// (the kernel completes the rename), so this measures the weaker property the
// kill can prove: never a torn file, never a corrupt read.
func TestKillDuringCheckpoint(t *testing.T) {
	vault := newVault(t)
	if _, err := run(t, vault, "checkpoint", "kestrel", "--next", "the first, complete checkpoint"); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	for i := 0; i < 40; i++ {
		cmd := exec.Command(bin(t), "checkpoint", "kestrel",
			"--next", fmt.Sprintf("attempt %d", i),
			"--failed", strings.Repeat("a long ruled-out approach that makes the write bigger. ", 200))
		cmd.Env = append(os.Environ(), "BRAIN_VAULT="+vault, "BRAIN_AGENT=chaos")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i%20) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()

		// Whatever survived, the vault must still read cleanly.
		out, err := run(t, vault, "sessions", "kestrel")
		if err != nil {
			t.Fatalf("iteration %d: the vault is unreadable after a kill: %v\n%s", i, err, out)
		}
		if strings.Contains(out, "panic") {
			t.Fatalf("iteration %d: reading after a kill panicked:\n%s", i, out)
		}
	}

	// No torn markdown anywhere, and no temp litter left visible.
	dir := filepath.Join(vault, "sessions", "kestrel")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("unreadable file left behind: %s: %v", e.Name(), err)
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			t.Logf("non-markdown left in the checkpoint directory: %s (%d bytes)", e.Name(), len(raw))
			continue
		}
		if !bytes.HasPrefix(raw, []byte("---\n")) {
			t.Errorf("%s does not start with frontmatter — a torn write is visible as a checkpoint", e.Name())
		}
	}
}

// Kill during index, repeatedly, then require the cache to rebuild cleanly.
func TestKillDuringIndex(t *testing.T) {
	vault := newVault(t)
	for i := 0; i < 300; i++ {
		body := fmt.Sprintf("---\ntype: topic\ntitle: Note %d\n---\n\n%s\n",
			i, strings.Repeat(fmt.Sprintf("content for note %d. ", i), 40))
		if err := os.WriteFile(filepath.Join(vault, fmt.Sprintf("note-%03d.md", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 10; i++ {
		cmd := exec.Command(bin(t), "index")
		cmd.Env = append(os.Environ(), "BRAIN_VAULT="+vault)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(5+i*7) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()
	}

	out, err := run(t, vault, "index")
	if err != nil {
		t.Fatalf("index could not recover after ten kills: %v\n%s", err, out)
	}
	if searchOut, err := run(t, vault, "search", "content for note 42"); err != nil {
		t.Errorf("search broken after killed indexes: %v\n%s", err, searchOut)
	} else if !strings.Contains(searchOut, "note-042") {
		t.Errorf("a note was lost after killed indexes:\n%s", searchOut)
	}
}

// The durability claim under a crash: memories written, process killed, cache
// deleted, reindexed. This is the README's own instruction under the worst
// timing.
func TestKillThenDeleteTheCache(t *testing.T) {
	vault := newVault(t)

	// Deliberately unrelated sentences: near-identical texts are collapsed by
	// semantic dedup at write time, which would be measured here as crash loss.
	facts := []string{
		"the waveguide costs 4.20 dollars per unit",
		"Sam runs the audio team out of Berlin",
		"the drop test threshold is 1.2 metres onto oak",
		"our Q3 pricing lands at 249 per seat",
		"the aluminium frame is what the drop test depends on",
		"standup moved to 09:15 on Tuesdays",
		"the factory in Shenzhen quotes 6 weeks lead time",
		"prefer terse replies with no preamble",
		"the display driver is the second most expensive line",
		"legal signed off on the open-source licence in March",
	}
	for _, f := range facts {
		if _, err := run(t, vault, "memory", "add", f); err != nil {
			t.Fatalf("memory add: %v", err)
		}
	}

	// A killed write while the store is being flushed.
	cmd := exec.Command(bin(t), "memory", "add", "the fact written while dying")
	cmd.Env = append(os.Environ(), "BRAIN_VAULT="+vault)
	cmd.Start()
	time.Sleep(3 * time.Millisecond)
	cmd.Process.Kill()
	cmd.Wait()

	if err := os.RemoveAll(filepath.Join(vault, ".brain")); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, vault, "index"); err != nil {
		t.Fatalf("reindex after cache deletion failed: %v\n%s", err, out)
	}

	out, err := run(t, vault, "memory")
	if err != nil {
		t.Fatalf("listing memories: %v\n%s", err, out)
	}
	var missing []string
	for _, f := range facts {
		if !strings.Contains(out, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d of %d memories did not survive kill + rm -rf .brain:\n  %s\n%s",
			len(missing), len(facts), strings.Join(missing, "\n  "), out)
	}
}

// Two processes on one vault, which is the normal deployment: a CLI run while
// the desktop app or an MCP server holds the same vault.
func TestTwoProcessesRacing(t *testing.T) {
	vault := newVault(t)
	if _, err := run(t, vault, "checkpoint", "kestrel", "--next", "seed"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	failures := make(chan string, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				out, err := run(t, vault, "note", "kestrel", fmt.Sprintf("writer %d line %d", i, j))
				if err != nil {
					failures <- fmt.Sprintf("writer %d: %v: %s", i, err, strings.TrimSpace(out))
				}
			}
		}(i)
	}
	wg.Wait()
	close(failures)

	var n int
	for f := range failures {
		if n < 5 {
			t.Errorf("concurrent write failed: %s", f)
		}
		n++
	}
	if n > 0 {
		t.Errorf("%d of 32 concurrent writes failed; two brain processes on one "+
			"vault is the normal case, not an edge case", n)
	}
}

// ENOSPC. A store that reports success on a full disk is a store that loses data.
func TestFullDisk(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses hdiutil; macOS only")
	}
	mount, cleanup := smallDisk(t, 8) // 8MB
	defer cleanup()

	vault := filepath.Join(mount, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, vault, "memory", "add", "the first fact, written with room to spare"); err != nil {
		t.Fatalf("seeding on a small disk: %v", err)
	}

	// Fill the remaining space. Block size matters: 1MB blocks stop with most of
	// a megabyte still free, which is plenty for a small memory file — the write
	// then succeeds and the test measures nothing.
	filler := filepath.Join(mount, "filler")
	f, err := os.Create(filler)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1 << 20, 4096, 512} {
		block := bytes.Repeat([]byte("x"), size)
		for {
			if _, err := f.Write(block); err != nil {
				break
			}
		}
	}
	f.Sync()
	f.Close()

	// The bar is that the failure is reported. Where it is reported from does not
	// matter — on a truly full disk SQLite usually fails first, which is a
	// perfectly good answer.
	out, err := run(t, vault, "memory", "add", "the fact written to a full disk")
	if err == nil {
		t.Errorf("writing a memory to a full disk reported success:\n%s", out)
	} else {
		t.Logf("full disk reported: %v\n%s", err, strings.TrimSpace(out))
	}

	// Free space and confirm the store is not corrupt.
	os.Remove(filler)
	if out, err := run(t, vault, "memory"); err != nil {
		t.Errorf("the store is unusable after a full-disk write: %v\n%s", err, out)
	} else if !strings.Contains(out, "first fact") {
		t.Errorf("the pre-existing memory was lost by a failed write:\n%s", out)
	}
}

// Checkpoint to a read-only vault: the failure must be reported, not swallowed.
func TestReadOnlyVault(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	vault := newVault(t)
	if _, err := run(t, vault, "checkpoint", "kestrel", "--next", "seed"); err != nil {
		t.Fatal(err)
	}

	sessions := filepath.Join(vault, "sessions", "kestrel")
	if err := os.Chmod(sessions, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sessions, 0o755)

	out, err := run(t, vault, "checkpoint", "kestrel", "--next", "this cannot be written")
	if err == nil {
		t.Errorf("checkpointing to a read-only directory reported success:\n%s", out)
	}
}

// smallDisk mounts a scratch disk image and returns its mount point.
func smallDisk(t *testing.T, megabytes int) (string, func()) {
	t.Helper()
	img := filepath.Join(t.TempDir(), "scratch.dmg")
	out, err := exec.Command("hdiutil", "create", "-size", fmt.Sprintf("%dm", megabytes),
		"-fs", "HFS+", "-volname", "brainchaos", "-quiet", img).CombinedOutput()
	if err != nil {
		t.Skipf("cannot create a disk image here: %v\n%s", err, out)
	}
	attach, err := exec.Command("hdiutil", "attach", img, "-nobrowse", "-quiet").CombinedOutput()
	if err != nil {
		t.Skipf("cannot attach the disk image: %v\n%s", err, attach)
	}
	mount := "/Volumes/brainchaos"
	sc := bufio.NewScanner(bytes.NewReader(attach))
	for sc.Scan() {
		if i := strings.Index(sc.Text(), "/Volumes/"); i >= 0 {
			mount = strings.TrimSpace(sc.Text()[i:])
		}
	}
	return mount, func() {
		exec.Command("hdiutil", "detach", mount, "-force", "-quiet").Run()
	}
}
