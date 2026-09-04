package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Two agents against one vault is this product's ordinary case, and they are
// not two goroutines: Claude Code and Cursor each launch their own `brain mcp
// serve`, in their own process, pointed at the same directory. The desktop app
// and a terminal make a third and fourth.
//
// A sync.Mutex cannot serialise those. It made the in-process case safe and
// left the case the product is actually sold on — hand a vault between the
// agents you already use — exactly as it was: one process reads
// memories/fact.md while another is between writing it and recording what it
// wrote, decides the rows it cannot see were lines the user deleted, and
// forgets them. Every writer is told the memory was saved.
//
// An advisory OS lock is the one mechanism that spans processes, and flock in
// particular is released by the kernel when the holder exits. That matters more
// than it sounds: a lock whose *existence on disk* is the lock has to be
// distinguished from one left by a crash, which means a staleness heuristic,
// which means a timeout that is either too short (two writers at once again) or
// too long (a vault wedged until it expires). There is no stale flock. A
// process that dies holding one releases it on the way out.
//
// The lock files live in .brain/ because .brain is already the throwaway half
// of the vault: the invariant every document ships is "delete the index, run
// `brain index`, lose nothing", and a lock is precisely the kind of state that
// must not survive into the durable half.

// lockSubdir keeps the sidecars out of .brain's top level, where index.db and
// its WAL live and where `brain doctor` enumerates what it expects to find.
const lockSubdir = "locks"

// inProc serialises goroutines within this process.
//
// The OS lock alone is not enough. flock is held per open file description, and
// two goroutines here open their own — on Linux they would each be granted the
// "same" exclusive lock and both proceed. So the mutex is not belt-and-braces;
// it is the half that covers threads, and flock is the half that covers
// processes. Neither one is sufficient alone.
var (
	inProcMu sync.Mutex
	inProc   = map[string]*sync.Mutex{}
)

// Guard is a held lock. Unlock is safe to call exactly once, and callers defer
// it immediately.
type Guard struct {
	mu     *sync.Mutex
	f      *os.File
	locked bool // whether the OS lock was actually taken, so Unlock matches
}

// Lock takes the vault-wide lock named name, blocking until it is free.
//
// name is a bare identifier — "memory-fact", "memory-pending" — so that writers
// to different files do not queue behind one another. Callers that need two
// locks must take them in a fixed order; today the only nesting is a kind lock
// outside the pending lock.
//
// A vault on a filesystem with no working flock (some network mounts) is not a
// reason to refuse to save a memory, so the OS half degrades to the in-process
// half and says so on stderr rather than failing the write. It is announced
// because a silently weaker guarantee is how this class of bug got here.
func Lock(vaultDir, name string) (*Guard, error) {
	dir := filepath.Join(vaultDir, ".brain", lockSubdir)
	if err := MkdirPrivate(dir); err != nil {
		return nil, fmt.Errorf("creating the lock directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, name+".lock")

	inProcMu.Lock()
	mu, ok := inProc[path]
	if !ok {
		mu = &sync.Mutex{}
		inProc[path] = mu
	}
	inProcMu.Unlock()

	mu.Lock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, FileMode)
	if err != nil {
		mu.Unlock()
		return nil, fmt.Errorf("opening the lock file %s: %w", path, err)
	}
	g := &Guard{mu: mu, f: f}
	if err := lockFD(f); err != nil {
		warnOnce(path, err)
		return g, nil // in-process only; see the doc comment
	}
	g.locked = true
	return g, nil
}

// Unlock releases both halves, in the reverse order they were taken.
func (g *Guard) Unlock() {
	if g == nil {
		return
	}
	if g.locked {
		unlockFD(g.f)
	}
	// Close before releasing the mutex: on the platforms where the lock rides on
	// the descriptor, closing is what makes it available to the next process,
	// and doing it after would let a goroutine here win a lock this process has
	// not yet given up.
	g.f.Close()
	g.mu.Unlock()
}

// warnOnce reports a degraded lock a single time per path. Repeating it on
// every memory written would bury the host's log in the same line.
var (
	warnedMu sync.Mutex
	warned   = map[string]bool{}
)

func warnOnce(path string, err error) {
	warnedMu.Lock()
	defer warnedMu.Unlock()
	if warned[path] {
		return
	}
	warned[path] = true
	// stderr, never stdout: stdout is the MCP JSON-RPC transport.
	fmt.Fprintf(os.Stderr,
		"brain: %s cannot be locked (%v) — writes are serialised within this "+
			"process only; run one agent at a time against this vault\n", path, err)
}
