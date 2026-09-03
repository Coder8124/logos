// Package announce decides how loudly Logos says what it just did.
//
// The problem it solves is a product one, not a technical one. Everything Logos
// does happens next to somebody else's work: an agent stores a fact in the
// middle of a refactor, a checkpoint lands as a session closes. Done silently,
// all of it is invisible, and a tool whose work is invisible is one users
// assume is broken — they stop trusting it long before they can say why.
//
// Done too loudly it is worse. A banner on every single call trains people to
// skip our output entirely, and then the messages that matter — a write that
// failed, a queue nobody is draining — go by unread with all the rest.
//
// So: a short marked receipt on the things a person would want to know about,
// nothing on the rest, and a switch for anyone who disagrees.
//
//	LOGOS_ANNOUNCE=off     say nothing extra
//	LOGOS_ANNOUNCE=quiet   the facts, without the marker
//	LOGOS_ANNOUNCE=on      the default: marker plus facts
//
// The environment variable is checked on every call rather than cached. These
// are long-lived server processes, and a setting you have to restart to change
// is one people give up on instead.
package announce

import (
	"os"
	"path/filepath"
	"strings"

	vaultpkg "github.com/Coder8124/brain/internal/vault"
)

// Level is how much to say.
type Level int

const (
	Off Level = iota
	Quiet
	On
)

// Env is the variable that overrides the stored setting.
const Env = "LOGOS_ANNOUNCE"

// File is where `brain announce` stores a persistent choice, relative to the
// vault. A file rather than a shell export because the people most annoyed by
// the receipts are running Logos inside an editor, where there is no shell to
// export from.
const File = ".brain/announce"

// Marker leads every receipt. One glyph and one word, chosen so a person
// scanning a transcript can find our lines without reading them, and so a
// script can grep for them.
const Marker = "✓ Logos"

// Setting resolves the level. The environment wins over the stored file, which
// wins over the default — the usual order, and the one a user expects when they
// set a variable specifically to override what is configured.
func Setting(vault string) Level {
	if l, ok := parse(os.Getenv(Env)); ok {
		return l
	}
	if vault != "" {
		if b, err := os.ReadFile(filepath.Join(vault, File)); err == nil {
			if l, ok := parse(string(b)); ok {
				return l
			}
		}
	}
	return On
}

func parse(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "0", "false", "no", "silent":
		return Off, true
	case "quiet", "plain", "bare":
		return Quiet, true
	case "on", "1", "true", "yes", "full":
		return On, true
	}
	return On, false
}

// Store writes a persistent choice. Passing On removes the file rather than
// writing "on" into it, so a vault that has never been configured and one
// explicitly set back to the default look the same on disk.
func Store(vault string, l Level) error {
	path := filepath.Join(vault, File)
	if l == On {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := vaultpkg.MkdirPrivate(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(l.String()+"\n"), vaultpkg.FileMode)
}

func (l Level) String() string {
	switch l {
	case Off:
		return "off"
	case Quiet:
		return "quiet"
	}
	return "on"
}

// Say builds a receipt: the marker, then what happened.
//
// At Off it returns the empty string, and callers that have nothing else to say
// should return that unchanged — an empty tool result is better than a tool
// result whose only content is a heading.
func Say(vault, what string) string {
	return At(Setting(vault), what)
}

// At is Say with the level already resolved, for callers making several
// receipts in a row who should not re-read the setting for each.
func At(l Level, what string) string {
	what = strings.TrimSpace(what)
	switch l {
	case Off:
		return ""
	case Quiet:
		return what
	}
	if what == "" {
		return ""
	}
	return Marker + " · " + what
}
