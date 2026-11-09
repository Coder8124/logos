package session

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/vault"
)

// IsClaimPath reports whether rel (vault-relative, slash-separated) names a
// checkpoint or plan: the two files reserved by an empty O_EXCL create of
// their final name and filled by a write that comes after (claimCheckpoint,
// claimPlan). The name is what makes them unique across processes, so the
// reservation cannot move to a temp name; a kill between the two steps
// leaves the empty file instead.
func IsClaimPath(rel string) bool {
	if !strings.HasPrefix(rel, CheckpointDir+"/") {
		return false
	}
	name := path.Base(rel)
	return IsCheckpointFile(name) || (strings.HasPrefix(name, "plan-") && strings.HasSuffix(name, ".md"))
}

// isAbandonedClaim is a claim still empty after vault.StaleTempAge. A live
// claim is empty for the milliseconds between the reservation and the write,
// or longer under the same stalls a temp file waits out — and removing it
// then would hand its name to a second writer.
func isAbandonedClaim(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Size() == 0 && vault.IsStaleTemp(info.ModTime())
}

// IsAbandonedClaim reports whether the file at rel is a claim a killed write
// left empty, for index's walk to collect.
func IsAbandonedClaim(rel string, info os.FileInfo) bool {
	return IsClaimPath(rel) && isAbandonedClaim(info)
}

// RemoveAbandonedClaims removes the empty checkpoints and plans killed writes
// left (#380). An empty one has nothing to lose, but it was read as a
// checkpoint with no frontmatter and sat in every sync client and git status
// for good. Each is checked again before removal, since the walk that found
// it may be minutes old; it keeps going past a failure, as RemoveTemps does,
// so the ones it did remove can still be said.
func RemoveAbandonedClaims(vaultDir string, rels []string) (removed []string, err error) {
	var errs []error
	for _, rel := range rels {
		p := filepath.Join(vaultDir, filepath.FromSlash(rel))
		info, serr := os.Lstat(p)
		if serr != nil || !IsAbandonedClaim(rel, info) {
			continue // gone, or written since: no longer an abandoned claim
		}
		if rerr := os.Remove(p); rerr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, rerr))
			continue
		}
		removed = append(removed, rel)
	}
	return removed, errors.Join(errs...)
}
