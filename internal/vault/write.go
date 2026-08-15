package vault

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes via a temp file in the same directory then renames, so a
// crash mid-write cannot leave a truncated note and Obsidian never observes a
// partial file.
//
// Every path that puts a file into the vault goes through here — the rollup
// applying an accepted proposal, and an agent committing a checkpoint. One
// writer means one set of guarantees, rather than each caller reinventing how
// carefully it wants to be interrupted.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".brain-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
