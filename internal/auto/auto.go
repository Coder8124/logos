// Package auto is what Logos does without being asked, in one place.
//
// Each automatic behaviour grew its own switch or none — the session-end
// checkpoint, the distil offer, the edit hook — so nobody could see or change
// "what Logos does without me" without reading the code. Here every one of them
// is a named switch, and the answer lives in auto.md at the vault's root: a
// file the user can read and edit by hand, which a deleted index cannot reset
// (invariant 1).
//
// Three states per switch, as with the activity log, because there are three
// different users. One who turned a switch off by hand meant it, and `logos
// auto on` must not undo that. One who said `auto on` or `auto off` meant all
// of it. One who never answered gets the default, and the default is on.
//
// On is the default for every switch because nobody ever turns one on: a
// switch that shipped off was a feature that shipped dark, so Logos looked like
// it did less than it does, and the dream, the index rebuild and the import
// that would have helped never ran for the people they were written for. The
// switches exist to turn things off. What is never automatic is not a switch
// at all but Fixed, which no setting changes.
package auto

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	vaultpkg "github.com/Coder8124/logos/internal/vault"
)

// File is the switch file, at the vault's root.
const File = "auto.md"

// Path is where a vault's switches are written down.
func Path(vault string) string { return filepath.Join(vault, File) }

// Switch names one automatic behaviour.
type Switch string

const (
	Checkpoint    Switch = "checkpoint"
	Distil        Switch = "distil"
	RulingsAtEdit Switch = "rulings-at-edit"
	Dream         Switch = "dream"
	Index         Switch = "index"
	Review        Switch = "review"
	Import        Switch = "import"
	// Archive keeps a copy of each transcript Logos reads, in the vault, so the
	// conversation outlives the host's own pruning. Redact decides whether
	// that copy has its secrets and <private> spans masked on the way in.
	Archive Switch = "archive"
	Redact  Switch = "redact"
)

type spec struct {
	name Switch
	what string
	// def is what a vault nobody has asked gets. It is on for every switch:
	// see the package comment.
	def bool
}

var specs = []spec{
	{Checkpoint, "write a checkpoint for a session that ended without one", true},
	{Distil, "offer an unclosed session's transcript to the next agent to read", true},
	{RulingsAtEdit, "show the rulings on a file when an agent is about to edit it", true},
	// A dream spends a local model's time, and with no model configured it is
	// a silent no-op, so on costs nobody who has no model anything.
	{Dream, "run logos memory dream in the background once a day, at session start", true},
	// On, though before this a missing index was said, not rebuilt: the
	// rebuild is text only and cheap, and a deleted index answering "nothing
	// matches" reads as a vault that lost everything. A hook never rebuilds
	// (LOGOS_HOOK), since a large vault can outlast its timeout, so on costs no
	// hook its work.
	{Index, "rebuild a missing index from the vault, text only, before the command that found it", true},
	// On, and rarely fires, by design: a contested proposal is never accepted
	// for the user, and under the default an agent's proposal queues only when
	// it is contested — so what it accepts comes from LOGOS_REVIEW_ALL queues.
	// An import writes another tool's summaries into memory, and the first
	// `logos import` asked before writing any; on only re-imports a store
	// that was already imported by hand.
	{Import, "re-import claude-mem's store at session start when it has changed, skipping what Logos has already seen", true},
	{Review, "accept a queued memory that cites a live transcript turn, holds no redaction, contests nothing, and is a decision or a procedure with a trap (rarely fires: contested ones always wait for you)", true},
	// Without a copy
	// a host that prunes or rotates its sessions takes the only record of what
	// they said, and nobody who never heard of this switch will ever turn it on.
	{Archive, "keep a copy of each transcript Logos reads in the vault, so it outlives the host's own pruning", true},
	// On for the same reason, and because the copy is the one place a pasted
	// secret would otherwise land in a folder that is often under version
	// control. Off keeps the copy raw, byte for byte.
	{Redact, "mask secrets and <private> spans in the archived copy of a transcript", true},
}

// Switches lists every switch, in the order status prints them.
func Switches() []Switch {
	out := make([]Switch, len(specs))
	for i, s := range specs {
		out[i] = s.name
	}
	return out
}

// Describe says what a switch does, in a phrase.
func Describe(s Switch) string {
	for _, sp := range specs {
		if sp.name == s {
			return sp.what
		}
	}
	return ""
}

func known(s Switch) (spec, bool) {
	for _, sp := range specs {
		if sp.name == s {
			return sp, true
		}
	}
	return spec{}, false
}

// Fixed is what is never automatic, whatever auto.md says, with the reason.
// Listed so nobody has to wonder, and refused by name so a hand-written
// `- verify: on` is answered rather than silently ignored.
var Fixed = []struct{ Name, Why string }{
	{"verify", "a procedure's verify command is vault text, and vault text is never run as an instruction"},
	{"forget", "forgetting or retiring a memory loses something only the user can judge"},
	{"retire", "forgetting or retiring a memory loses something only the user can judge"},
	{"push", "nothing pushes or syncs without the user"},
	{"sync", "nothing pushes or syncs without the user"},
	{"network", "nothing leaves the machine"},
}

func fixedWhy(name string) (string, bool) {
	for _, f := range Fixed {
		if f.Name == name {
			return f.Why, true
		}
	}
	return "", false
}

// Config is auto.md read. Master is nil when nobody has said `auto on` or
// `auto off`; Hand holds the switches set one at a time. Problems are lines the
// file holds that mean nothing, each one said by status rather than dropped.
type Config struct {
	Master *bool
	Hand   map[Switch]bool
	// Said holds the notices already shown for this vault ("said: <topic>"
	// lines). They live here and not in the index so that deleting the index
	// cannot make Logos repeat a notice, or forget one it owes (invariant 1).
	Said     map[string]bool
	Problems []string
}

// On is whether a switch is on under this config.
func (c Config) On(s Switch) bool {
	if v, ok := c.Hand[s]; ok {
		return v
	}
	if c.Master != nil {
		return *c.Master
	}
	sp, _ := known(s)
	return sp.def
}

// Why says what decided a switch, for status.
func (c Config) Why(s Switch) string {
	switch {
	case hasKey(c.Hand, s):
		return "set by hand"
	case c.Master != nil && *c.Master:
		return "auto on"
	case c.Master != nil:
		return "auto off"
	}
	return "default"
}

func hasKey(m map[Switch]bool, s Switch) bool { _, ok := m[s]; return ok }

var (
	masterLine = regexp.MustCompile(`^auto:\s*(\S+)\s*$`)
	saidLine   = regexp.MustCompile(`^said:\s*([A-Za-z][\w-]*)\b.*$`)
	switchLine = regexp.MustCompile(`^[-*]\s*([A-Za-z][\w-]*)\s*:\s*(\S+)\s*$`)
)

func parseValue(v string) (bool, bool) {
	switch strings.ToLower(v) {
	case "on", "yes", "true":
		return true, true
	case "off", "no", "false":
		return false, true
	}
	return false, false
}

// Load reads a vault's switches. A missing file is a vault nobody has asked,
// not an error. An unreadable one is an error, which status reports.
func Load(vault string) (Config, error) {
	c := Config{Hand: map[Switch]bool{}, Said: map[string]bool{}}
	raw, err := os.ReadFile(Path(vault))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	for _, line := range strings.Split(stripFrontmatter(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if m := masterLine.FindStringSubmatch(line); m != nil {
			v, ok := parseValue(m[1])
			if !ok {
				c.Problems = append(c.Problems, fmt.Sprintf("line %q: auto is on or off", line))
				continue
			}
			c.Master = &v
			continue
		}
		if m := saidLine.FindStringSubmatch(line); m != nil {
			c.Said[strings.ToLower(m[1])] = true
			continue
		}
		m := switchLine.FindStringSubmatch(line)
		if m == nil {
			continue // prose and headings are the user's own
		}
		name := strings.ToLower(m[1])
		if why, ok := fixedWhy(name); ok {
			c.Problems = append(c.Problems, fmt.Sprintf("%s is never automatic: %s", name, why))
			continue
		}
		if _, ok := known(Switch(name)); !ok {
			c.Problems = append(c.Problems, fmt.Sprintf("%s is not a switch (%s); ignored", name, strings.Join(names(), ", ")))
			continue
		}
		v, ok := parseValue(m[2])
		if !ok {
			c.Problems = append(c.Problems, fmt.Sprintf("%s: %q is not on or off; ignored", name, m[2]))
			continue
		}
		c.Hand[Switch(name)] = v
	}
	return c, nil
}

func names() []string {
	var out []string
	for _, s := range specs {
		out = append(out, string(s.name))
	}
	return out
}

// stripFrontmatter drops a leading --- block, whose `type:` line would
// otherwise read as nothing worse than prose but whose shape is the user's.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if end := strings.Index(s[4:], "\n---"); end >= 0 {
		return s[4+end+4:]
	}
	return s
}

// On is whether a switch is on in this vault. A file that exists and cannot be
// read is somebody's answer that cannot be seen, so every switch reads as off
// until it can: guessing the defaults is how a user who turned everything off
// finds it running. Status reports the read failure.
func On(vault string, s Switch) bool {
	c, err := Load(vault)
	if err != nil {
		return false
	}
	return c.On(s)
}

// SetMaster records `auto on` or `auto off`. A switch set by hand keeps its
// value: the user who turned one off meant that one, whatever they say about
// the rest later.
func SetMaster(vault string, on bool) error {
	return edit(vault, func(c *Config) error {
		c.Master = &on
		return nil
	})
}

// Said reports whether the notice named topic was already shown in this vault.
func Said(vault, topic string) bool {
	c, err := Load(vault)
	return err == nil && c.Said[strings.ToLower(topic)]
}

// MarkSaid records that a notice was shown, as a line in auto.md. Written
// after the notice is printed, so a crash between the two repeats a notice
// rather than losing one.
func MarkSaid(vault, topic string) error {
	return edit(vault, func(c *Config) error {
		c.Said[strings.ToLower(topic)] = true
		return nil
	})
}

// Set records one switch by hand.
func Set(vault, name string, on bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if why, ok := fixedWhy(name); ok {
		return fmt.Errorf("%s is never automatic: %s", name, why)
	}
	if _, ok := known(Switch(name)); !ok {
		return fmt.Errorf("%s is not a switch; the switches are %s", name, strings.Join(names(), ", "))
	}
	return edit(vault, func(c *Config) error {
		c.Hand[Switch(name)] = on
		return nil
	})
}

// edit changes auto.md in place: the lines Logos owns are rewritten and
// everything else the user wrote there is kept, so a note in their own words
// beside a switch survives the next `logos auto on`.
func edit(vault string, change func(*Config) error) error {
	g, err := vaultpkg.Lock(vault, "auto")
	if err != nil {
		return err
	}
	defer g.Unlock()

	c, err := Load(vault)
	if err != nil {
		return err
	}
	if err := change(&c); err != nil {
		return err
	}
	raw, err := os.ReadFile(Path(vault))
	if errors.Is(err, os.ErrNotExist) {
		raw = []byte(fresh)
	} else if err != nil {
		return err
	}
	return vaultpkg.WriteAtomic(Path(vault), []byte(render(string(raw), c)))
}

const fresh = `---
type: logos-auto
---
# What Logos does on its own

Edit this by hand or with ` + "`logos config auto`" + `. A switch on its own line wins over
the ` + "`auto:`" + ` line; ` + "`logos config auto status`" + ` says what each one is doing.

`

func render(raw string, c Config) string {
	lines := strings.Split(raw, "\n")
	seen := map[Switch]bool{}
	saidSeen := map[string]bool{}
	sawMaster := false
	inFront := strings.HasPrefix(raw, "---\n")
	for i, line := range lines {
		if inFront {
			if i > 0 && strings.TrimSpace(line) == "---" {
				inFront = false
			}
			continue
		}
		t := strings.TrimSpace(line)
		if m := saidLine.FindStringSubmatch(t); m != nil {
			saidSeen[strings.ToLower(m[1])] = true
			continue
		}
		if masterLine.MatchString(t) && c.Master != nil {
			lines[i] = "auto: " + onOff(*c.Master)
			sawMaster = true
			continue
		}
		if m := switchLine.FindStringSubmatch(t); m != nil {
			s := Switch(strings.ToLower(m[1]))
			// Every line for it, not the first: Load reads the last, so a
			// file holding a switch twice would keep its old value.
			if v, ok := c.Hand[s]; ok {
				lines[i] = "- " + string(s) + ": " + onOff(v)
				seen[s] = true
			}
		}
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	if c.Master != nil && !sawMaster {
		out += "\nauto: " + onOff(*c.Master) + "\n"
	}
	var told []string
	for topic := range c.Said {
		if !saidSeen[topic] {
			told = append(told, "said: "+topic)
		}
	}
	sort.Strings(told)
	if len(told) > 0 {
		out += "\n" + strings.Join(told, "\n") + "\n"
	}
	var missing []string
	for s, v := range c.Hand {
		if !seen[s] {
			missing = append(missing, "- "+string(s)+": "+onOff(v))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		out += "\n" + strings.Join(missing, "\n") + "\n"
	}
	return out
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
