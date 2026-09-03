package vault

import (
	"os"
	"path/filepath"
	"strings"
)

// Where the vault lives, and how every front end agrees on it.
//
// There were two rules. The CLI defaulted to ~/brain; the desktop app kept its
// own copy defaulting to ~/brain-vault. index.Open creates the directory it is
// pointed at, so the app made the wrong vault on first launch and then reported
// a healthy zero of everything — indistinguishable from a working install with
// nothing in it, while the user's actual memory sat one directory away.
//
// Collapsing the two into one function fixed the default but not the general
// case, because the escape hatch was an environment variable. A .app launched
// from Finder or Spotlight inherits no login shell: BRAIN_VAULT exported from a
// profile is invisible to it. So anyone whose vault is not at the default had a
// desktop app that could not be pointed at it at all.
//
// A location a person chose has to be written down somewhere both a terminal
// and a GUI can read. That is what Record does, and it is why the search order
// below has three steps instead of two.

// pointerName is the file holding the chosen vault path, inside the user's
// config directory (~/Library/Application Support/brain on macOS, ~/.config/brain
// elsewhere). Deliberately not inside the vault: a pointer stored in the place
// it points at cannot be found by anyone who does not already know where that is.
const pointerName = "vault-path"

// Path resolves where the vault lives. One answer, for every front end.
//
// The order is explicit override, then recorded choice, then default:
//
//  1. BRAIN_VAULT, which is how an MCP host config pins a server to one vault
//     and how a scratch vault is used in a test or a shell. An explicit
//     instruction in the current process wins over anything on disk.
//  2. The path `brain setup` recorded. This is what makes the desktop app find
//     a non-default vault, since it has no environment to inherit.
//  3. ~/brain. Absolute, because a relative default is resolved against
//     whatever directory a host happened to launch the binary from — which is
//     the original version of this bug.
func Path() string {
	if v := os.Getenv("BRAIN_VAULT"); v != "" {
		return v
	}
	if v := Recorded(); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "brain")
	}
	return "vault" // no home directory to speak of; the old behaviour
}

// Recorded returns the vault path written by Record, or "" if there is none.
//
// A recorded path that no longer exists is ignored rather than returned. The
// pointer is a convenience, not a second source of truth: if someone moved or
// deleted their vault, falling back to the default and creating a fresh one is
// wrong, but so is insisting on a directory that is not there — the caller gets
// the default and the "no vault at …" message that names it.
func Recorded() string {
	p, err := pointerPath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(raw))
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// Record writes down which vault this machine uses, so a front end with no
// environment to inherit can still find it. Called by `brain setup` once the
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
	return WriteAtomic(p, []byte(abs+"\n"))
}

func pointerPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "brain", pointerName), nil
}
