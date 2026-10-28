package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A vault is often a folder iCloud Drive, Dropbox or Syncthing keeps in step
// between two machines. When both machines write the same file before a sync,
// the client keeps both and gives the loser a new name beside the original:
// "x 2.md", "x (conflicted copy).md", "x (Sam's conflicted copy 2026-10-28).md",
// "x.sync-conflict-20261028-101500-ABCDEFG.md". Read as a file of its own, that
// copy was a second checkpoint with the same session, the same ruled-out
// approaches and the same next step: `show --history` listed one checkpoint
// three times, `tried` cited one dead end three times, and search ranked the
// copies against the original.
//
// Recognised only where logos generates the names — sessions/ and memories/,
// whose files are timestamps and uids that never contain a space — because a
// person's own "Plan 2.md" beside "Plan.md" is a real note, and hiding it from
// search would be a loss no announcement makes up for. And only when the
// original is still there: a copy whose original was deleted is the only
// record left, so it is read like any other file.

var (
	// iCloud Drive: a space and a number, from 2.
	icloudCopy = regexp.MustCompile(`^(.+) ([2-9]|[1-9][0-9]+)\.md$`)
	// Dropbox: a parenthesised "conflicted copy", with or without a name and date.
	dropboxCopy = regexp.MustCompile(`^(.+) \([^()]*conflicted copy[^()]*\)\.md$`)
	// Syncthing: ".sync-conflict-<date>-<time>-<device>" before the extension.
	syncthingCopy = regexp.MustCompile(`^(.+)\.sync-conflict-[0-9]{8}-[0-9]{6}-[A-Z0-9]+\.md$`)
)

// conflictOriginal returns the filename a sync client's conflict copy was
// made from, or "" when name is not shaped like one.
func conflictOriginal(name string) string {
	for _, re := range []*regexp.Regexp{dropboxCopy, syncthingCopy, icloudCopy} {
		if m := re.FindStringSubmatch(name); m != nil {
			return m[1] + ".md"
		}
	}
	return ""
}

// IsConflictCopy reports whether name, a file in dir, is a sync client's
// conflict copy whose original is still beside it — the case where reading
// the copy counts the original twice. The original is what is read; the copy
// is left on disk untouched for the person to compare and delete.
func IsConflictCopy(dir, name string) bool {
	orig := conflictOriginal(name)
	if orig == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, orig))
	return err == nil
}

// ConflictCopies lists, relative to the vault, every conflict copy that
// readers of sessions/ and memories/ set aside, so `logos index` can say how
// many it left out and which. Silence would make the copy look either read or
// lost, and it is neither: it is waiting for someone to merge it by hand.
func ConflictCopies(vaultDir string) []string {
	var out []string
	for _, top := range []string{"sessions", "memories"} {
		root := filepath.Join(vaultDir, top)
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if IsConflictCopy(filepath.Dir(path), d.Name()) {
				if rel, err := filepath.Rel(vaultDir, path); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
			return nil
		})
	}
	return out
}
