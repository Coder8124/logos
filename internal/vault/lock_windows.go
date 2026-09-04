//go:build windows

package vault

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows has no flock. LockFileEx is the equivalent, and it is mandatory
// rather than advisory — which is fine here, because the only writers are this
// program and the lock file holds no content anyone reads.
//
// The lock covers a one-byte range at offset zero rather than the whole file:
// locking a zero-length file's whole range is a no-op on Windows, and the
// sidecar is always zero length.
func lockFD(f *os.File) error {
	// LOCKFILE_EXCLUSIVE_LOCK without LOCKFILE_FAIL_IMMEDIATELY: blocking, for
	// the reason the unix side gives.
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0,
		new(windows.Overlapped),
	)
}

func unlockFD(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
