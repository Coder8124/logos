package health

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A checkpoint the probe writes does not always land directly under
// sessions/<project>/: worktree scoping (internal/mcpserver/scope.go's
// scopeName) files it one level deeper, at
// sessions/<project>/<worktree>/<id>.md, whenever the process running the
// probe — brain doctor --integration itself — has its working directory
// inside a linked git worktree. That is not a hypothetical: an agent working
// on brain in an isolated worktree is exactly this case.
//
// cleanUp only ever tried to remove the project directory, so it found the
// now-empty worktree subdirectory sitting inside it, called the project
// directory "not empty", and reported a probe that fully succeeded as a
// cleanup failure.
func TestCleanUpRemovesAWorktreeScopedCheckpointsDirectory(t *testing.T) {
	vault := t.TempDir()
	root := filepath.Join(vault, "sessions", "brain-selftest-1234")
	nested := filepath.Join(root, "some-worktree")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(nested, "20260101-000000-probe.md")
	if err := os.WriteFile(checkpoint, []byte("project: brain-selftest-1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cleanUp(vault, "brain-selftest-1234", checkpoint); err != nil {
		t.Fatalf("cleanUp on a worktree-scoped checkpoint returned an error instead of removing it: %v", err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Errorf("worktree subdirectory %s still exists after cleanup", nested)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("project directory %s still exists after cleanup", root)
	}
}

// The safety property this must not lose: if something the probe did not
// write is sitting in the same directory as its checkpoint, cleanUp must
// leave it alone and say so, rather than deleting a directory that might
// hold somebody's real notes.
func TestCleanUpLeavesAForeignFileInTheWorktreeDirectoryAlone(t *testing.T) {
	vault := t.TempDir()
	root := filepath.Join(vault, "sessions", "brain-selftest-5678")
	nested := filepath.Join(root, "some-worktree")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(nested, "20260101-000000-probe.md")
	if err := os.WriteFile(checkpoint, []byte("project: brain-selftest-5678\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(nested, "someones-real-note.md")
	if err := os.WriteFile(foreign, []byte("not the probe's"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cleanUp(vault, "brain-selftest-5678", checkpoint); err == nil {
		t.Fatal("cleanUp reported success while a foreign file was still in the directory")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("a foreign file was removed or its directory destroyed: %v", err)
	}
}

// Integration's stdout reader pushes every scanned line into a 64-entry
// buffered channel. If the consumer stops reading before the child process
// stops writing — the exact shape of a probe that got the answer it needed
// from an early message and never called await again — the 65th line blocks
// the send forever. Nothing before this test gave that goroutine a way out,
// so it outlives Integration itself for as long as the process runs `brain
// doctor`.
func TestScanStdoutStopsWhenNobodyIsReadingTheChannel(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for i := 0; i < 200; i++ {
			fmt.Fprintf(w, "line %d\n", i)
		}
	}()

	sc := bufio.NewScanner(r)
	out := make(chan string, 4) // deliberately never drained past its buffer
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		scanStdout(sc, out, stop)
		close(done)
	}()

	// Give it time to fill the buffer and block on the send, exactly the state
	// a probe that stopped reading early leaves it in.
	time.Sleep(50 * time.Millisecond)
	close(stop)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scanStdout is still blocked on the channel send after stop was closed — the goroutine leak")
	}
}
