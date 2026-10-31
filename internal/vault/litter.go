package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// tempName is exactly what WriteAtomic's os.CreateTemp pattern produces, so a
// sweep can never take a file a person or another tool named.
var tempName = regexp.MustCompile(`^\.logos-[0-9]+\.tmp$`)

// StaleTempAge is how old a WriteAtomic temp file must be before it is
// removed. Not a minute, though a healthy write holds its temp file for
// milliseconds: a writer can stall with the file open for far longer and
// still be about to rename it — a laptop that slept mid-write, a process
// under SIGSTOP or a debugger, an fsync waiting on iCloud or a network
// disk. Removing its temp file then fails a write that would have
// succeeded. An hour outlasts any stall short of a crash, and a crash's
// litter waiting an extra hour costs nothing but a hidden file.
const StaleTempAge = time.Hour

// IsTempName reports whether name is a WriteAtomic temp file.
func IsTempName(name string) bool { return tempName.MatchString(name) }

// IsStaleTemp reports whether a temp file last modified at mod is old enough
// that no live write can own it.
func IsStaleTemp(mod time.Time) bool { return time.Since(mod) > StaleTempAge }

// RemoveTemps removes the temp files WriteAtomic leaves when its process is
// killed between creating one and renaming it — the deferred Remove never
// runs on SIGKILL, so every interrupted write left one behind for good,
// hidden in Finder but uploaded by every sync client and listed by git
// status in a tracked vault. rels are vault-relative, as index.Sync reports
// them. Each is checked again before removal, since the list is from a walk
// that may be minutes old.
//
// It keeps going past a failure and returns both what it removed and every
// error, because stopping at the first left the files it had already
// removed unannounced: a caller that only saw the error could not say them.
func RemoveTemps(vaultDir string, rels []string) (removed []string, err error) {
	var errs []error
	for _, rel := range rels {
		p := filepath.Join(vaultDir, filepath.FromSlash(rel))
		info, serr := os.Lstat(p)
		if serr != nil || !IsTempName(info.Name()) || !IsStaleTemp(info.ModTime()) {
			continue // gone already, or written to since: no longer litter
		}
		if rerr := os.Remove(p); rerr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, rerr))
			continue
		}
		removed = append(removed, rel)
	}
	return removed, errors.Join(errs...)
}
