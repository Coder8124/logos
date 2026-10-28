package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// tempName is exactly what WriteAtomic's os.CreateTemp pattern produces, so a
// sweep can never take a file a person or another tool named.
var tempName = regexp.MustCompile(`^\.logos-[0-9]+\.tmp$`)

// StaleTempAge is how old a WriteAtomic temp file must be before a sweep takes
// it. A live write holds its temp file for milliseconds; a minute is far past
// any write still in flight, so the sweep cannot pull a file out from under a
// second logos process mid-rename.
const StaleTempAge = time.Minute

// SweepTempLitter removes the temp files WriteAtomic leaves when its process
// is killed between creating one and renaming it. The deferred Remove never
// runs on SIGKILL, and nothing else ever looked for them, so every interrupted
// write left one behind for good — hidden in Finder, but uploaded by every
// sync client and listed by git status in a tracked vault. It returns the
// vault-relative paths it removed so the caller can say so.
func SweepTempLitter(vaultDir string, olderThan time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-olderThan)
	var removed []string
	var firstErr error
	filepath.WalkDir(vaultDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != vaultDir && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !tempName.MatchString(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if rel, err := filepath.Rel(vaultDir, path); err == nil {
			removed = append(removed, filepath.ToSlash(rel))
		}
		return nil
	})
	return removed, firstErr
}
