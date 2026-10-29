package vault

import (
	"os"
	"path/filepath"
	"regexp"
)

// A vault is often a folder iCloud Drive, Dropbox or Syncthing keeps in step
// between two machines. When both machines write the same file before a sync,
// the client keeps both and gives the loser a new name beside the original:
// "x 2.md", "x (conflicted copy).md", "x (Sam's conflicted copy 2026-10-28).md",
// "x.sync-conflict-20261028-101500-ABCDEFG.md", and on Google Drive or
// OneDrive "x (1).md". Read as a file of its own, that
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
// record left, so it is read like any other file. memories/ is the one
// exception: its originals are removed on purpose, and readKindFiles keeps a
// copy outliving one from bringing a forgotten memory back.

var (
	// iCloud Drive: a space and a number, from 2.
	icloudCopy = regexp.MustCompile(`^(.+) ([2-9]|[1-9][0-9]+)\.md$`)
	// Google Drive and OneDrive: a space and a parenthesised number, from 1.
	driveCopy = regexp.MustCompile(`^(.+) \([1-9][0-9]*\)\.md$`)
	// Dropbox: a parenthesised "conflicted copy", with or without a name and date.
	dropboxCopy = regexp.MustCompile(`^(.+) \([^()]*conflicted copy[^()]*\)\.md$`)
	// Syncthing: ".sync-conflict-<date>-<time>-<device>" before the extension.
	syncthingCopy = regexp.MustCompile(`^(.+)\.sync-conflict-[0-9]{8}-[0-9]{6}-[A-Z0-9]+\.md$`)
)

// conflictParent returns the filename a sync client's conflict copy was
// made from, or "" when name is not shaped like one.
func conflictParent(name string) string {
	for _, re := range []*regexp.Regexp{dropboxCopy, syncthingCopy, driveCopy, icloudCopy} {
		if m := re.FindStringSubmatch(name); m != nil {
			return m[1] + ".md"
		}
	}
	return ""
}

// conflictAncestors is every name a copy descends from, nearest first. A
// copy of a copy is ordinary: a vault synced by two clients, or a conflict
// that happened again before anyone merged the first, gives "x 2 (conflicted
// copy).md", and reading only one suffix off it found "x 2.md" — not on disk,
// so the copy was read as a checkpoint of its own.
func conflictAncestors(name string) []string {
	var out []string
	for p := conflictParent(name); p != "" && len(out) < 8; p = conflictParent(p) {
		out = append(out, p)
	}
	return out
}

// ConflictOriginal returns the root original a conflict copy was made from,
// through any number of copies of copies, or "" when name is not shaped like
// a copy at all.
func ConflictOriginal(name string) string {
	a := conflictAncestors(name)
	if len(a) == 0 {
		return ""
	}
	return a[len(a)-1]
}

// IsConflictCopy reports whether name, a file in dir, is a sync client's
// conflict copy of a file still beside it — the case where reading the copy
// counts that file twice. The original is what is read; the copy is left on
// disk untouched for the person to compare and delete.
func IsConflictCopy(dir, name string) bool {
	for _, a := range conflictAncestors(name) {
		if _, err := os.Stat(filepath.Join(dir, a)); err == nil {
			return true
		}
	}
	return false
}

// CopiesOf lists the conflict copies of name sitting in dir, so a caller that
// moves a file can move its copies with it. Left behind, a copy is no longer
// beside its original, and IsConflictCopy rightly reads a lone copy as the
// only record left — so moving the original alone turned one checkpoint
// into two.
func CopiesOf(dir, name string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, a := range conflictAncestors(e.Name()) {
			if a == name {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out, nil
}
