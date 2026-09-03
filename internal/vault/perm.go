package vault

import (
	"os"
	"path/filepath"
)

// The vault holds the user's private memory: what they told an assistant, who
// they work with, and every prompt they typed. It lives in their home
// directory, which on a shared machine is readable by everyone with an account
// unless something says otherwise.
//
// Nothing did. Notes came out 0600 only because os.CreateTemp happens to make
// files that way and WriteAtomic renames one into place — while the two files
// that hold *everything*, the activity log and index.db, were written 0644 by
// their own callers. So the careful-looking permission on the markdown was
// undone by a world-readable database containing the same text.
//
// These constants make the intent explicit rather than incidental, so a new
// writer inherits it instead of rediscovering it.
const (
	// FileMode is owner read/write, nothing for anyone else. Every file this
	// product creates inside the vault.
	FileMode os.FileMode = 0o600
	// DirMode is owner-only traversal. A directory that is group- or
	// world-executable lets anyone walk in and stat what is inside, which for a
	// vault leaks the project names and session times even when every file in
	// it is unreadable.
	DirMode os.FileMode = 0o700
)

// MkdirPrivate creates a directory the way the vault wants it.
//
// os.MkdirAll does not change the mode of a directory that already exists, so
// this does not silently retighten a vault the user set up deliberately — new
// directories are private, and existing ones are left as they are and reported
// by `brain doctor` instead. Tightening someone's filesystem without asking is
// its own kind of surprise.
func MkdirPrivate(dir string) error {
	return os.MkdirAll(dir, DirMode)
}

// Private sets FileMode on a path that already exists, ignoring absence.
//
// For files this package does not create itself — index.db, which the SQLite
// driver opens with its own mode. Ignoring a missing file matters because the
// caller uses this on paths the driver creates lazily.
func Private(path string) error {
	if err := os.Chmod(path, FileMode); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PrivateSiblings applies Private to a path and the sidecars a SQLite database
// keeps beside it. The -wal file is the one that matters most: it holds pages
// not yet checkpointed back, which is to say the most recent writes.
func PrivateSiblings(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := Private(p); err != nil {
			return err
		}
	}
	return nil
}

// EnsureDir creates a file's parent directory privately.
func EnsureDir(path string) error {
	return MkdirPrivate(filepath.Dir(path))
}
