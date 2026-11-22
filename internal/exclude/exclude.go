// Package exclude is how a person says "never read this conversation".
//
// Logos reads transcripts on its own: the sweep at resume, the session-end
// checkpoint, the archive, the automatic memories. Each of those is a read of
// something the person may never have meant to keep, and the only way out used
// to be turning the whole behaviour off. Here the answer is per project, per
// session, or for a stretch of time (a pause), and it lives in exclude.md at
// the vault's root — a file the user can read and edit, which a deleted index
// cannot reset (invariant 1).
//
// The check runs BEFORE a transcript is opened. A skip that happened after the
// read has already put the text in memory, in a prompt, or in a temp file, and
// "we read it but did not keep it" is not what an exclusion promises.
//
// Every uncertainty reads as excluded. A file that exists but cannot be read
// is somebody's answer that cannot be seen; a session whose folder is unknown
// cannot be proved to be outside an excluded project. Guessing "allowed" there
// is how a conversation the person excluded ends up archived.
package exclude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// File is the exclusion list, at the vault's root beside auto.md.
const File = "exclude.md"

// Path is where a vault's exclusions are written down.
func Path(vault string) string { return filepath.Join(vault, File) }

// The reasons a session is skipped, as a receipt says them.
const (
	ReasonProject  = "excluded project"
	ReasonSession  = "excluded session"
	ReasonNoFolder = "no known folder while a project is excluded"
	ReasonPaused   = "paused"
	ReasonUnread   = "exclusion list unreadable"
)

// minPrefix is how short a stored session id may be and still match the longer
// id a host uses. The id the user copies from `logos resume` is often the
// first eight characters; below that a prefix names too many conversations.
const minPrefix = 8

// keepEnded is how long a pause window outlives its end before the next edit
// drops it. A window that ended is still the reason a session from inside it
// stays skipped, so it is not dropped the moment it ends.
const keepEnded = 30 * 24 * time.Hour

// Window is a pause: no session whose activity overlaps [From, Until] is read.
type Window struct{ From, Until time.Time }

// Rules is exclude.md read.
type Rules struct {
	Projects []string // absolute, symlinks resolved
	Sessions []string
	Pauses   []Window
}

var (
	projectLine = regexp.MustCompile(`^[-*]\s*project:\s*(\S.*?)\s*$`)
	sessionLine = regexp.MustCompile(`^[-*]\s*session:\s*(\S+)\s*$`)
	pauseLine   = regexp.MustCompile(`^[-*]\s*pause:\s*from=(\S+)\s+until=(\S+)\s*$`)
)

// Load reads a vault's exclusions. A missing file is a vault with none. An
// unreadable one is an error, which Check turns into a skip.
func Load(vault string) (Rules, error) {
	var r Rules
	raw, err := os.ReadFile(Path(vault))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	for _, line := range strings.Split(stripFrontmatter(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if m := projectLine.FindStringSubmatch(line); m != nil {
			r.Projects = addUnique(r.Projects, m[1])
		} else if m := sessionLine.FindStringSubmatch(line); m != nil {
			r.Sessions = addUnique(r.Sessions, m[1])
		} else if m := pauseLine.FindStringSubmatch(line); m != nil {
			from, e1 := time.Parse(time.RFC3339, m[1])
			until, e2 := time.Parse(time.RFC3339, m[2])
			if e1 == nil && e2 == nil {
				r.Pauses = append(r.Pauses, Window{from, until})
			}
			// A pause line that does not parse is dropped here and rewritten
			// away by the next edit; an exclusion that cannot be read as a
			// window protects nothing, and `--list` shows what did load.
		}
		// Anything else is the user's own prose.
	}
	return r, nil
}

func addUnique(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if end := strings.Index(s[4:], "\n---"); end >= 0 {
		return s[4+end+4:]
	}
	return s
}

// Normalize is a folder as it is stored and compared: absolute, with symlinks
// resolved. /tmp and /private/tmp are one folder on a Mac, and an exclusion
// typed with one spelling must catch a session that ran under the other. A path
// that no longer exists is cleaned, not rejected: excluding a folder that was
// deleted since is still a promise about its old sessions.
func Normalize(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("no folder given")
	}
	if strings.ContainsAny(p, "\r\n") {
		return "", errors.New("a folder name cannot contain a line break")
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	// The folder is gone, or part of it is. Resolve the deepest ancestor that
	// exists so a deleted subfolder of a symlinked parent still lands on the
	// same spelling a live session would have.
	dir, rest := abs, ""
	for dir != filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest), nil
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	return filepath.Clean(abs), nil
}

// Subject is what is about to be read. Any field may be unknown. Start and End
// bound the activity in the session (End zero means Start; both zero means
// "now", which is what a pause protects).
type Subject struct {
	Cwd        string
	ID         string
	Start, End time.Time
}

// Check says why a session must not be read, or "" when it may. now is passed
// so a test can hold the clock.
func Check(vault string, s Subject) string { return CheckAt(vault, s, time.Now()) }

// CheckAt is Check with an explicit clock.
func CheckAt(vault string, s Subject, now time.Time) string {
	r, err := Load(vault)
	if err != nil {
		return ReasonUnread
	}
	return r.reason(s, now)
}

func (r Rules) sessionExcluded(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, x := range r.Sessions {
		if x == id || (len(x) >= minPrefix && strings.HasPrefix(id, x)) {
			return true
		}
	}
	return false
}

// CheckID is the session half of Check, for a caller that holds an id and
// nothing else (a host's own session id in a hook payload or a candidate).
func CheckID(vault, id string) string {
	r, err := Load(vault)
	if err != nil {
		return ReasonUnread
	}
	if r.sessionExcluded(id) {
		return ReasonSession
	}
	return ""
}

func (r Rules) reason(s Subject, now time.Time) string {
	if r.sessionExcluded(s.ID) {
		return ReasonSession
	}
	if len(r.Projects) > 0 {
		if strings.TrimSpace(s.Cwd) == "" {
			return ReasonNoFolder
		}
		cwd, err := Normalize(s.Cwd)
		if err != nil {
			return ReasonNoFolder
		}
		for _, p := range r.Projects {
			if cwd == p || strings.HasPrefix(cwd, strings.TrimRight(p, string(filepath.Separator))+string(filepath.Separator)) {
				return ReasonProject
			}
		}
	}
	start, end := s.Start, s.End
	if start.IsZero() && end.IsZero() {
		start, end = now, now
	} else if end.IsZero() {
		end = start
	} else if start.IsZero() {
		start = end
	}
	for _, w := range r.Pauses {
		if !end.Before(w.From) && !start.After(w.Until) {
			return ReasonPaused
		}
	}
	return ""
}

// ActivePause is the pause in force at now, if any.
func (r Rules) ActivePause(now time.Time) (Window, bool) {
	var best Window
	found := false
	for _, w := range r.Pauses {
		if !now.Before(w.From) && now.Before(w.Until) && (!found || w.Until.After(best.Until)) {
			best, found = w, true
		}
	}
	return best, found
}

// Skips counts what was not read, by reason, so the command that skipped says
// how many (invariant 3). The zero value is ready.
type Skips struct{ by map[string]int }

// Add records one skipped session.
func (k *Skips) Add(reason string) {
	if k.by == nil {
		k.by = map[string]int{}
	}
	k.by[reason]++
}

// Merge folds another count in.
func (k *Skips) Merge(o Skips) {
	for r, n := range o.by {
		for i := 0; i < n; i++ {
			k.Add(r)
		}
	}
}

// N is how many sessions were skipped.
func (k Skips) N() int {
	n := 0
	for _, c := range k.by {
		n += c
	}
	return n
}

// Line is the receipt: "1 session skipped (excluded project)", or "" when
// nothing was skipped.
func (k Skips) Line() string {
	n := k.N()
	if n == 0 {
		return ""
	}
	reasons := make([]string, 0, len(k.by))
	for r := range k.by {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	noun := "sessions"
	if n == 1 {
		noun = "session"
	}
	if len(reasons) == 1 {
		return fmt.Sprintf("%d %s skipped (%s)", n, noun, reasons[0])
	}
	parts := make([]string, len(reasons))
	for i, r := range reasons {
		parts[i] = fmt.Sprintf("%d %s", k.by[r], r)
	}
	return fmt.Sprintf("%d %s skipped (%s)", n, noun, strings.Join(parts, ", "))
}

// Skipped is the error a checkpoint writer returns for a session it was told
// not to read, so a caller can tell "excluded" from "failed" and count it.
type Skipped struct{ Reason string }

func (e *Skipped) Error() string { return "session skipped (" + e.Reason + ")" }

// IsSkipped reports whether err is a Skipped, and its reason.
func IsSkipped(err error) (string, bool) {
	var s *Skipped
	if errors.As(err, &s) {
		return s.Reason, true
	}
	return "", false
}

// --- editing -------------------------------------------------------------

const fresh = `---
type: logos-exclude
---
# What Logos will not read

Edit this by hand or with ` + "`logos exclude`" + ` and ` + "`logos pause`" + `. A ` + "`project:`" + ` covers its
subfolders; ` + "`logos exclude --list`" + ` says what is in force.

`

func owned(line string) bool {
	t := strings.TrimSpace(line)
	return projectLine.MatchString(t) || sessionLine.MatchString(t) || pauseLine.MatchString(t)
}

// edit changes exclude.md in place: the lines Logos owns are rewritten and the
// user's own prose is kept, as auto.md does.
func edit(vault string, change func(*Rules) error) error {
	g, err := vaultpkg.Lock(vault, "exclude")
	if err != nil {
		return err
	}
	defer g.Unlock()

	r, err := Load(vault)
	if err != nil {
		return err
	}
	if err := change(&r); err != nil {
		return err
	}
	raw, err := os.ReadFile(Path(vault))
	if errors.Is(err, os.ErrNotExist) {
		raw = []byte(fresh)
	} else if err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !owned(line) {
			keep = append(keep, line)
		}
	}
	out := strings.TrimRight(strings.Join(keep, "\n"), "\n") + "\n\n"
	sort.Strings(r.Projects)
	sort.Strings(r.Sessions)
	for _, p := range r.Projects {
		out += "- project: " + p + "\n"
	}
	for _, s := range r.Sessions {
		out += "- session: " + s + "\n"
	}
	cut := time.Now().Add(-keepEnded)
	for _, w := range r.Pauses {
		if w.Until.Before(cut) {
			continue
		}
		out += "- pause: from=" + w.From.UTC().Format(time.RFC3339) + " until=" + w.Until.UTC().Format(time.RFC3339) + "\n"
	}
	return vaultpkg.WriteAtomic(Path(vault), []byte(out))
}

// AddProject excludes a folder and everything under it. It returns the stored
// (absolute, resolved) spelling.
func AddProject(vault, dir string) (string, error) {
	p, err := Normalize(dir)
	if err != nil {
		return "", err
	}
	return p, edit(vault, func(r *Rules) error {
		r.Projects = addUnique(r.Projects, p)
		return nil
	})
}

// AddSession excludes one conversation by its host id.
func AddSession(vault, id string) error {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, " \t\r\n/\\") {
		return fmt.Errorf("%q is not a session id", id)
	}
	return edit(vault, func(r *Rules) error {
		r.Sessions = addUnique(r.Sessions, id)
		return nil
	})
}

// Remove lifts an exclusion named by folder or by session id, and says which
// kind it was. A name that is neither is an error: a silent no-op there reads
// as the exclusion having been lifted.
func Remove(vault, name string) (kind string, err error) {
	name = strings.TrimSpace(name)
	err = edit(vault, func(r *Rules) error {
		var keepS []string
		for _, s := range r.Sessions {
			if s == name {
				kind = "session"
				continue
			}
			keepS = append(keepS, s)
		}
		r.Sessions = keepS
		if kind != "" {
			return nil
		}
		p, perr := Normalize(name)
		var keepP []string
		for _, x := range r.Projects {
			if x == name || (perr == nil && x == p) {
				kind = "project"
				continue
			}
			keepP = append(keepP, x)
		}
		r.Projects = keepP
		if kind == "" {
			return fmt.Errorf("%q is not an excluded project or session (`logos exclude --list` shows them)", name)
		}
		return nil
	})
	return kind, err
}

// Pause opens a window of d starting now, and returns it. A pause is added to,
// not replaced: two pauses are two windows.
func Pause(vault string, d time.Duration, now time.Time) (Window, error) {
	if d <= 0 {
		return Window{}, errors.New("a pause needs a length greater than zero")
	}
	w := Window{From: now.Truncate(time.Second), Until: now.Add(d).Truncate(time.Second)}
	return w, edit(vault, func(r *Rules) error {
		r.Pauses = append(r.Pauses, w)
		return nil
	})
}

// Resume ends every pause in force at now, early. The window is shortened to
// now rather than deleted: a session that was active during the pause stays
// skipped, which is the point of pausing. It returns how many it ended.
func Resume(vault string, now time.Time) (int, error) {
	n := 0
	err := edit(vault, func(r *Rules) error {
		for i, w := range r.Pauses {
			if !now.Before(w.From) && now.Before(w.Until) {
				r.Pauses[i].Until = now.Truncate(time.Second)
				n++
			}
		}
		return nil
	})
	return n, err
}
