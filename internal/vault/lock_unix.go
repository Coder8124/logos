//go:build !windows

package vault

import (
	"os"
	"syscall"
)

// flock rather than fcntl/POSIX record locks, deliberately.
//
// POSIX locks are keyed by (process, inode) and are dropped when the process
// closes *any* descriptor on that file — including one opened and closed by an
// unrelated part of the same program. In a binary that also opens SQLite, a
// desktop app and a file watcher against the same vault, that is a lock which
// silently disappears. flock is keyed by the open file description, so it lives
// exactly as long as the descriptor this package holds.
func lockFD(f *os.File) error {
	// LOCK_EX without LOCK_NB: blocking is what callers want. A memory write
	// waiting a few milliseconds behind another agent's write is correct; failing
	// it because someone else was mid-flush is the behaviour this replaces.
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFD(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
