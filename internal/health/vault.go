package health

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/vault"
)

// The vault is the product. If it is missing or unwritable, nothing else
// matters, so this runs first and says exactly which of the two it is.
func checkVault(dir string) Check {
	c := Check{Name: "vault"}
	if strings.TrimSpace(dir) == "" {
		c.State, c.Detail = Failed, "no vault path resolved"
		c.Fix = "set LOGOS_VAULT, or run `logos setup`"
		return c
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		c.State, c.Detail = Failed, dir+" does not exist"
		c.Fix = "run `logos setup --vault " + shellArg(dir) + "`"
		// This machine's recorded vault being absent is usually an unmounted
		// drive, and setup at that path makes an empty vault where it mounts.
		if os.Getenv("LOGOS_VAULT") == "" && dir == vault.Pointer() {
			c.Detail = dir + " does not exist — it is the vault recorded for this machine"
			c.Fix = "reconnect the drive it is on; to use a different vault, run `logos setup --vault <path>`"
		}
		return c
	}
	if err != nil {
		c.State, c.Detail = Failed, err.Error()
		return c
	}
	if !info.IsDir() {
		c.State, c.Detail = Failed, dir+" is a file, not a directory"
		return c
	}
	// Readable is not enough: logos writes checkpoints here, and finding that
	// out at handoff time is finding out too late.
	probe := filepath.Join(dir, ".logos-write-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		c.State, c.Detail = Failed, dir+" is not writable: "+err.Error()
		c.Fix = "check permissions; checkpoints cannot be saved"
		return c
	}
	os.Remove(probe)

	// The vault is writable and real. One question left: is it the vault the
	// user meant, or a scratch directory that outlived the command that made it?
	//
	// `logos setup --vault <dir>` records its target in
	// os.UserConfigDir()/logos/vault-path, and that pointer is what every front
	// end reads when LOGOS_VAULT is unset — including a host launched from
	// Finder, which inherits no shell and has no other way to find the vault. So running setup
	// against a scratch vault, which CONTRIBUTING.md tells contributors to do,
	// silently repoints the real installation at a temporary directory. Nothing
	// then fails: index.Open creates whatever it is handed, so the vault is
	// present, writable and empty, and every check downstream honestly reports
	// zero. The author's own pointer named a /var/folders temp path for a day
	// while doctor called it healthy.
	//
	// The recorded pointer is what is checked, not the resolved directory. An
	// explicit LOGOS_VAULT is a deliberate choice scoped to one command and is
	// nobody's business to complain about; the pointer outlives the session.
	if rec := vault.Recorded(); rec != "" && UnderTempDir(rec) {
		c.State = Failed
		c.Detail = rec + " is a temporary directory, recorded as the vault every front end opens — it will be empty or gone"
		c.Fix = "run `logos setup --vault <your real vault>` to repoint it, or `logos doctor` with LOGOS_VAULT set to check a scratch vault without recording it"
		return c
	}

	// A blocklist of temporary roots is always one directory short: the pointer
	// that actually did the damage named ~/.claude/jobs/<id>/tmp/survey-vault,
	// which is not a system temp root at all, and UnderTempDir walked straight
	// past it. So ask the question that does not depend on knowing where the
	// next harness will put its scratch directories.
	//
	// Neither half is a fault on its own. A vault with no checkpoints is a
	// perfectly good new install, and history in a second vault is a perfectly
	// good second vault. It is the pair that means the pointer is wrong — and
	// the pair is precisely what the user cannot see, because every command
	// they run reads the empty one and truthfully reports nothing.
	//
	// Scoped to the recorded pointer for the same reason the check above is: an
	// explicit LOGOS_VAULT is a scratch vault someone chose for this one
	// command, and it is supposed to be empty. Complaining about it would make
	// the documented workflow print a failure on every run.
	if os.Getenv("LOGOS_VAULT") == "" {
		if other, n := populatedVaultElsewhere(dir); n > 0 {
			c.State = Failed
			c.Detail = fmt.Sprintf("no checkpoints here, but %s holds %d — every front end is reading this empty vault instead", other, n)
			c.Fix = "run `logos setup --vault " + shellArg(other) + "` to repoint this machine"
			return c
		}
	}

	c.State, c.Detail = OK, dir
	return c
}

// populatedVaultElsewhere reports another vault on disk that has history in it,
// when the vault in use has none. Only the default location is looked at: it is
// where a vault is unless somebody moved it, and searching the disk for vaults
// would be a slow answer to a question doctor asks on every run.
func populatedVaultElsewhere(dir string) (string, int) {
	// Only "is there any", so stop at the first one. This runs on every doctor.
	if checkpointCount(dir, 1) > 0 {
		return "", 0
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", 0
	}
	def := filepath.Join(home, "logos")
	for _, a := range forms(def) {
		for _, b := range forms(dir) {
			if a == b {
				return "", 0
			}
		}
	}
	// The real total here: it is printed, and "28 checkpoints sit in ~/logos" is
	// the number that tells the user which vault is the one they meant.
	if n := checkpointCount(def, 0); n > 0 {
		return def, n
	}
	return "", 0
}

// checkpointCount totals the checkpoints across every project in a vault,
// reading the markdown rather than the index — the index of the vault nobody is
// using is exactly the one that will not be built.
//
// stopAt bounds the work for the caller that only needs "is there any": names
// are counted rather than files parsed, because doctor runs this on every
// invocation over two vaults, and parsing a user's entire history to answer a
// yes/no question makes the command slower the longer they have used it.
func checkpointCount(dir string, stopAt int) int {
	// Scopes, so doctor's "is there any work here" answer is not no on a vault
	// whose every checkpoint was written from a git worktree.
	projects, err := session.Scopes(dir)
	if err != nil {
		return 0
	}
	total := 0
	for _, p := range projects {
		entries, err := os.ReadDir(filepath.Join(dir, session.CheckpointDir, p))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !session.IsCheckpointFile(e.Name()) {
				continue
			}
			total++
			if stopAt > 0 && total >= stopAt {
				return total
			}
		}
	}
	return total
}

// UnderTempDir reports whether path sits inside a system temporary directory.
//
// Every well-known temp root is checked, not just os.TempDir(). os.TempDir()
// answers $TMPDIR, which on macOS is a per-user directory under /var/folders —
// and the directory that actually repointed a real installation was under
// /tmp, which shares no prefix with it. Agent scratchpads, mktemp -d scripts
// and half the shell in this repository use /tmp; a guard that cannot see it is
// a guard against the one case that has never happened.
//
// Both sides are resolved through symlinks first: /tmp answers to /private/tmp
// and /var/folders/... to /private/var/folders/..., and a string compare of the
// two forms says they are unrelated.
func UnderTempDir(path string) bool {
	// An agent's per-job scratch directory is a temporary root that lives under
	// $HOME, so none of the system roots below match it. ~/.claude/jobs/<id>/tmp
	// is where the pointer that broke a real installation was made.
	roots := []string{os.TempDir(), "/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "jobs"), filepath.Join(home, ".claude", "tmp"))
	}
	for _, p := range forms(path) {
		for _, root := range roots {
			for _, t := range forms(root) {
				// Separator-anchored, so /tmpfoo is not read as living under /tmp.
				if p == t || strings.HasPrefix(p, t+string(filepath.Separator)) {
					return true
				}
			}
		}
	}
	return false
}

// forms returns the spellings of a path that have to be compared: the cleaned
// path, and its symlink-resolved form when it has one. Both are needed because
// only one side of the comparison usually exists on disk — EvalSymlinks("/tmp")
// yields /private/tmp, but EvalSymlinks("/tmp/a-vault-that-was-deleted") fails
// and leaves the literal spelling, so resolving only what resolves would make
// the two halves disagree about the same directory.
func forms(path string) []string {
	path = filepath.Clean(path)
	out := []string{path}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if real = filepath.Clean(real); real != path {
			out = append(out, real)
		}
	}
	return out
}

// checkPrivacy reports what the rest of the machine can read.
//
// Everything Logos knows lives in one directory in the user's home. On a
// personal laptop that is nobody but them; on a shared box, a work machine with
// a management agent, or anything with another account on it, the mode bits are
// the only thing standing between a second user and every prompt the first one
// typed. That is worth one line in `logos doctor` whether or not it is worth
// worrying about, because "who can read this" is not a question you can answer
// by looking at the app.
//
// It reports rather than repairs. New files and directories are created private
// (see internal/vault.FileMode), but a vault that predates that, or one the user
// deliberately opened up to sync it, is theirs — silently chmod-ing somebody's
// filesystem is exactly the kind of unrequested help this product does not do.
// So: name the paths, give the command, let them decide.
func checkPrivacy(dir string) Check {
	c := Check{Name: "privacy"}
	if strings.TrimSpace(dir) == "" {
		c.State, c.Detail = Unknown, "no vault path resolved"
		return c
	}
	info, err := os.Stat(dir)
	if err != nil {
		c.State, c.Detail = Unknown, "vault not readable: "+err.Error()
		return c
	}
	// Name what is actually exposed rather than the directory alone. "0755 on a
	// folder" means nothing to most people; "your prompt log and the database
	// holding every note" means something.
	//
	// The contents are checked even when the directory itself is locked down,
	// and that is the whole point of the list. index.Open sets the mode on
	// index.db advisorily — `_ = vault.PrivateSiblings(...)`, with a comment
	// naming this check as what would catch a failure — so answering from the
	// directory's mode alone reported "readable only by you" over a 0644 copy of
	// every note, memory and checkpoint in the vault. A private directory is
	// also one chmod, one sync client or one backup away from not being one.
	var open []string
	for _, rel := range []string{"activity", ".logos/index.db", ".logos/index.db-wal", "memories", "sessions"} {
		p := filepath.Join(dir, rel)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.Mode().Perm()&0o077 != 0 {
			open = append(open, rel)
		}
	}
	if info.Mode().Perm()&0o077 == 0 && len(open) == 0 {
		c.State, c.Detail = OK, "the vault is readable only by you"
		return c
	}

	c.State = Failed
	if info.Mode().Perm()&0o077 != 0 {
		c.Detail = fmt.Sprintf("%s is readable by other users on this machine", dir)
		if len(open) > 0 {
			c.Detail += " — so is " + strings.Join(open, ", ")
		}
	} else {
		// The directory is closed but something inside it is not. Say so
		// precisely: the fix is the same command, but "your vault is fine except
		// for the file holding all of it" is a different sentence.
		c.Detail = fmt.Sprintf("%s is private, but %s inside it %s readable by other users on this machine",
			dir, strings.Join(open, ", "), isAre(len(open)))
	}
	c.Fix = "run `chmod -R go-rwx " + shellArg(dir) + "` if this machine has other accounts on it"
	return c
}
