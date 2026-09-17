package vault

import (
	"os"
	"path/filepath"
	"strings"
)

// Where the vault lives, and how every front end agrees on it.
//
// There were two rules. The CLI defaulted to ~/logos; the desktop app kept its
// own copy defaulting to ~/logos-vault. index.Open creates the directory it is
// pointed at, so the app made the wrong vault on first launch and then reported
// a healthy zero of everything — indistinguishable from a working install with
// nothing in it, while the user's actual memory sat one directory away.
//
// Collapsing the two into one function fixed the default but not the general
// case, because the escape hatch was an environment variable. A .app launched
// from Finder or Spotlight inherits no login shell: LOGOS_VAULT exported from a
// profile is invisible to it. So anyone whose vault is not at the default had a
// desktop app that could not be pointed at it at all.
//
// A location a person chose has to be written down somewhere both a terminal
// and a GUI can read. That is what Record does, and it is why the search order
// below has three steps instead of two.

// pointerName is the file holding the chosen vault path, inside the user's
// config directory (~/Library/Application Support/logos on macOS, ~/.config/logos
// elsewhere). Deliberately not inside the vault: a pointer stored in the place
// it points at cannot be found by anyone who does not already know where that is.
const pointerName = "vault-path"

// backupSuffix names the copy of the pointer taken before it is overwritten.
const backupSuffix = ".bak"

// Path resolves where the vault lives. One answer, for every front end.
//
// The order is explicit override, then recorded choice, then default:
//
//  1. LOGOS_VAULT, which is how an MCP host config pins a server to one vault
//     and how a scratch vault is used in a test or a shell. An explicit
//     instruction in the current process wins over anything on disk.
//  2. The path `logos setup` recorded. This is what makes the desktop app find
//     a non-default vault, since it has no environment to inherit.
//  3. ~/logos. Absolute, because a relative default is resolved against
//     whatever directory a host happened to launch the binary from — which is
//     the original version of this bug.
func Path() string {
	if v := os.Getenv("LOGOS_VAULT"); v != "" {
		return v
	}
	if v := Pointer(); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "logos")
	}
	return "vault" // no home directory to speak of; the old behaviour
}

// Chosen reports the vault path and whether anybody actually chose it — set
// LOGOS_VAULT in this process, or recorded a path with `logos setup`. When
// neither is true the answer is the ~/logos default, which nobody has vouched
// for, and a caller may treat "it is not there" as "make it" rather than as a
// mistake to report.
func Chosen() (dir string, explicit bool) {
	if v := os.Getenv("LOGOS_VAULT"); v != "" {
		return v, true
	}
	if v := Pointer(); v != "" {
		return v, true
	}
	return Path(), false
}

// Pointer returns the vault path written by Record, or "" if there is none —
// whether or not that directory is there right now.
//
// It used to be ignored when the directory was missing, falling back to
// ~/logos. A missing recorded vault is most often on a drive that is not
// mounted, and the fallback split one project across two vaults: the MCP server
// created ~/logos and accepted checkpoints into it, and after the drive came
// back `resume` found none of them. So the recorded path stays the answer, and
// every caller that opens a vault checks it exists first and refuses by name.
// Someone who really did delete it moves the pointer with `logos setup --vault`.
func Pointer() string {
	p, err := pointerPath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Recorded is Pointer, but only when the directory exists. For callers that are
// describing the recorded vault rather than opening it.
func Recorded() string {
	dir := Pointer()
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// Record writes down which vault this machine uses, so a front end with no
// environment to inherit can still find it. Called by `logos setup` once the
// vault is known.
//
// Failure is returned rather than swallowed, but callers treat it as a warning:
// a machine where the config directory cannot be written still has a working
// vault, it just has one the desktop app can only find if it is at the default.
func Record(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	p, err := pointerPath()
	if err != nil {
		return err
	}
	if err := MkdirPrivate(filepath.Dir(p)); err != nil {
		return err
	}
	// Keep whatever the pointer said before this. Moving a machine's vault is
	// one atomic write over one file, and the path it replaced is not written
	// down anywhere else — so a move made by a script, or by a setup run the
	// user was not reading closely, left nothing to go back to. Best effort: a
	// config directory that cannot hold the backup still gets the move.
	if prev := Pointer(); prev != "" && prev != abs {
		_ = WriteAtomic(p+backupSuffix, []byte(prev+"\n"))
	}
	return WriteAtomic(p, []byte(abs+"\n"))
}

// Previous returns the vault path the pointer held before the most recent
// Record, or "" if this machine has only ever had one.
func Previous() string {
	p, err := pointerPath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(p + backupSuffix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func pointerPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "logos", pointerName), nil
}
