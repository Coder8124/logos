package vault

import (
	"os"
	"path/filepath"
	"strings"
)

// brainIgnoreLine is what EnsureGitignore adds. The trailing slash matches
// only the directory, not some unrelated file that happens to be named
// .brain — the same shape git itself recommends for a directory rule.
const brainIgnoreLine = ".brain/"

// EnsureGitignore makes sure a vault that lives inside a git repository never
// offers .brain/ — the SQLite cache index.db rebuilds from markdown on every
// `brain index` — to be committed. It reports whether it wrote anything, so a
// caller can announce the change rather than let it happen silently.
//
// Two clones of the same vault each keep their own cache: it is a local
// index, not shared state, and a binary file with no information the
// markdown doesn't already carry is exactly the kind of thing that turns
// every `git pull` between two people sharing a vault into a merge conflict
// for no reason. This is the fix from the "vault two people can share over
// git" plan: markdown is truth, and the cache never touches git at all.
//
// It only ever appends. A .gitignore a user wrote for their own reasons keeps
// every line they put there, and a second run that finds .brain/ already
// covered — by this exact line or one they wrote themselves — changes
// nothing, so `brain index` calling this every time never turns into a diff
// on a file nobody meant to edit.
func EnsureGitignore(vaultDir string) (wrote bool, err error) {
	path := filepath.Join(vaultDir, ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	for _, line := range strings.Split(string(existing), "\n") {
		// Substring rather than exact-line match: ".brain" (no slash) or
		// "/.brain/" written by hand already does the job, and re-adding our
		// own line on top of a rule that already covers it is exactly the
		// needless churn this function exists to avoid.
		if strings.Contains(strings.TrimSpace(line), ".brain") {
			return false, nil
		}
	}

	next := string(existing)
	if len(next) > 0 && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += brainIgnoreLine + "\n"

	// 0644, not FileMode: this file is meant to be read by git and by whoever
	// else opens the repo, not private vault content like a note or a memory.
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
