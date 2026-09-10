package contextpack

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/vault"
)

// A tree view of the vault is only a control surface if toggling a node is
// durable across a rebuild, the same promise every other feature here makes.
// The rules therefore live as one hand-editable markdown file, not a table:
// what rebuilds this from markdown is nothing, because the markdown *is* the
// only copy. There is no cache to fall out of sync with and nothing for
// `rm -rf .brain` to take with it.
//
// The file sits under a dot-directory (.context/) rather than notes/ or
// alongside it, for the same reason memories/ and ingest/ are walked past by
// Sync: it is control-plane state about the vault, not prose to search. It
// does not need a new entry in that walk's skip list, because Sync already
// skips any directory whose name starts with ".".

// PathPin is PinAlways/PinNone/PinNever (see package memory) lifted from a
// single memory row to a vault path prefix, so a whole directory — an entire
// project's sessions, one memory kind's file, an ingest candidate nobody has
// triaged yet — can be steered the same way one memory can.
type PathPin int

const (
	PathPinNone   PathPin = iota // no rule; ranked/retrieved normally
	PathPinAlways                // forced into the pack regardless of ranking
	PathPinNever                 // held out of the pack entirely
)

// PathRule pins or excludes everything under Prefix, a slash-separated path
// relative to the vault root (e.g. "memories/preference.md", "sessions/old",
// "notes/design").
type PathRule struct {
	Prefix string  `json:"prefix"`
	Pin    PathPin `json:"pin"`
}

// contextRulesDir is dot-prefixed so index.Sync's existing "skip any directory
// starting with '.'" rule keeps it out of search without this package needing
// to reach into internal/index to add a case for it.
const contextRulesDir = ".context"
const contextRulesFile = "rules.md"

// RulesPath is where the tree view's pin/exclude state lives, for a caller
// that wants to show or watch the file directly.
func RulesPath(vaultDir string) string {
	return filepath.Join(vaultDir, contextRulesDir, contextRulesFile)
}

const rulesHeader = `# Context rules

Each line pins or excludes everything under one vault path from context packs.
Edit or delete any line by hand; brain reads this file directly and keeps
nothing about it anywhere else, so there is nothing else to keep in sync.

Format: "- pin: <path>" always includes it, "- exclude: <path>" never does.
A path is a prefix, relative to the vault root, e.g. "memories/preference.md"
or "sessions/old-project".
`

// LoadPathRules reads the rules file. A missing file is not an error — it is
// the state of a vault nobody has pinned or excluded anything in yet — and
// returns an empty, nil-error result so callers do not have to special-case
// os.IsNotExist themselves.
func LoadPathRules(vaultDir string) ([]PathRule, error) {
	f, err := os.Open(RulesPath(vaultDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var rules []PathRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		var kind, path string
		switch {
		case strings.HasPrefix(line, "pin:"):
			kind, path = "pin", strings.TrimSpace(strings.TrimPrefix(line, "pin:"))
		case strings.HasPrefix(line, "exclude:"):
			kind, path = "exclude", strings.TrimSpace(strings.TrimPrefix(line, "exclude:"))
		default:
			continue // prose, blank lines, the header — not a rule
		}
		path = strings.Trim(path, "`\"")
		if path == "" {
			continue
		}
		pin := PathPinAlways
		if kind == "exclude" {
			pin = PathPinNever
		}
		rules = append(rules, PathRule{Prefix: path, Pin: pin})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

// SetPathRule pins, excludes, or (Pin == PathPinNone) clears the rule for one
// path prefix, and writes the whole file back through vault.WriteAtomic —
// the vault, not a database row, is what this feature makes durable.
func SetPathRule(vaultDir, prefix string, pin PathPin) error {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return fmt.Errorf("no path given to pin or exclude")
	}
	rules, err := LoadPathRules(vaultDir)
	if err != nil {
		return fmt.Errorf("reading existing context rules: %w", err)
	}
	out := rules[:0]
	for _, r := range rules {
		if r.Prefix != prefix {
			out = append(out, r)
		}
	}
	if pin != PathPinNone {
		out = append(out, PathRule{Prefix: prefix, Pin: pin})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return vault.WriteAtomic(RulesPath(vaultDir), renderPathRules(out))
}

func renderPathRules(rules []PathRule) []byte {
	var b strings.Builder
	b.WriteString(rulesHeader)
	if len(rules) > 0 {
		b.WriteString("\n")
	}
	for _, r := range rules {
		verb := "pin"
		if r.Pin == PathPinNever {
			verb = "exclude"
		}
		fmt.Fprintf(&b, "- %s: %s\n", verb, r.Prefix)
	}
	return []byte(b.String())
}

// matchPathRule returns the most specific rule covering path — the longest
// matching prefix — so pinning "sessions/" and excluding
// "sessions/scratch-project" inside it behaves the way a file-manager
// override would: the narrower rule wins. PathPinNone means no rule matched.
func matchPathRule(path string, rules []PathRule) PathPin {
	best := PathPinNone
	bestLen := -1
	for _, r := range rules {
		// A trailing-slash match only: "sessions" must not match
		// "sessions-old", a sibling directory that merely shares a prefix of
		// characters rather than being nested under it.
		if path == r.Prefix || strings.HasPrefix(path, r.Prefix+"/") {
			if len(r.Prefix) > bestLen {
				bestLen, best = len(r.Prefix), r.Pin
			}
		}
	}
	return best
}

// memoryPathFor names the file a memory of this kind lives in — see
// internal/memory/vaultstore.go, which groups all memories of one kind into
// memories/<kind>.md rather than one file per memory. A path rule aimed at
// that file is the only prefix granularity a memory can be pinned at short of
// its own PinAlways/PinNever field.
func memoryPathFor(k memory.Kind) string {
	return memory.Dir + "/" + string(k) + ".md"
}

// applyPathRules enforces the tree view's pin/exclude state on an assembled
// pack. It runs after every other retrieval arm has filled in Notes and the
// memory sections, and before the budget spends them, so an excluded path
// never even competes for space and a pinned one is not at the mercy of
// ranking.
func (p *Pack) applyPathRules(ix *index.Index, rules []PathRule) {
	if len(rules) == 0 {
		return
	}

	// Notes: drop anything under an excluded prefix, then pull in anything
	// under a pinned prefix that ranking did not already surface.
	kept := p.Notes[:0]
	have := make(map[string]bool, len(p.Notes))
	for _, h := range p.Notes {
		if matchPathRule(h.Slug, rules) == PathPinNever {
			continue
		}
		kept = append(kept, h)
		have[h.Slug] = true
	}
	p.Notes = kept

	for _, r := range rules {
		if r.Pin != PathPinAlways {
			continue
		}
		// memories/ is handled below, by kind rather than by file slug —
		// pulling it through the notes path as well would double-count it.
		if strings.HasPrefix(r.Prefix, memory.Dir) {
			continue
		}
		for _, slug := range slugsUnder(ix.DB, r.Prefix) {
			if have[slug] {
				continue
			}
			// Checkpoints are excluded from Notes everywhere else in Build
			// (withoutSessionNotes) because they have their own section and
			// double-printing one undoes the reasoning applied to it. A pin
			// on a sessions/ path must not reopen that door.
			if strings.HasPrefix(slug, session.CheckpointDir+"/") {
				continue
			}
			if h, ok := ix.HitBySlug(slug); ok {
				h.Via = "pinned"
				p.Notes = append(p.Notes, h)
				have[slug] = true
			}
		}
	}

	p.applyMemoryPathRules(ix.DB, rules)
}

// slugsUnder lists every indexed note whose slug falls under prefix. Done as
// a direct query rather than a new internal/index method: contextpack already
// holds the same *sql.DB index.go's own Hit-lookup helpers use, and adding a
// third way to list notes to the index package for one caller is a worse cost
// than one query here.
func slugsUnder(db *sql.DB, prefix string) []string {
	rows, err := db.Query(`SELECT slug FROM notes WHERE slug = ? OR slug LIKE ?`, prefix, prefix+"/%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// applyMemoryPathRules is applyPathRules' memory half: a rule aimed at
// memories/<kind>.md excludes or force-pins every memory of that kind, on top
// of (never instead of) each memory's own Pin field — a memory a user
// individually excluded stays excluded even if its kind's folder is pinned,
// because the more specific, more deliberate choice should win.
func (p *Pack) applyMemoryPathRules(db *sql.DB, rules []PathRule) {
	var excludedKinds, pinnedKinds []memory.Kind
	for _, k := range []memory.Kind{memory.Preference, memory.Person, memory.Fact, memory.Context} {
		switch matchPathRule(memoryPathFor(k), rules) {
		case PathPinNever:
			excludedKinds = append(excludedKinds, k)
		case PathPinAlways:
			pinnedKinds = append(pinnedKinds, k)
		}
	}
	if len(excludedKinds) == 0 && len(pinnedKinds) == 0 {
		return
	}

	excluded := func(k memory.Kind) bool {
		for _, e := range excludedKinds {
			if e == k {
				return true
			}
		}
		return false
	}
	dropExcluded := func(mems []memory.Memory) []memory.Memory {
		out := mems[:0]
		for _, m := range mems {
			if !excluded(m.Kind) {
				out = append(out, m)
			}
		}
		return out
	}
	p.Preferences = dropExcluded(p.Preferences)
	p.Related = dropExcluded(p.Related)
	p.Pinned = dropExcluded(p.Pinned)

	if len(pinnedKinds) == 0 {
		return
	}
	have := make(map[int64]bool, len(p.Pinned))
	for _, m := range p.Pinned {
		have[m.ID] = true
	}
	all, err := memory.AllInProject(db, p.scope())
	if err != nil {
		return
	}
	for _, m := range all {
		if m.Pin == memory.PinNever || have[m.ID] {
			continue // a memory's own exclusion outranks a folder-level pin
		}
		pinnedByFolder := false
		for _, k := range pinnedKinds {
			if m.Kind == k {
				pinnedByFolder = true
				break
			}
		}
		if pinnedByFolder {
			p.Pinned = append(p.Pinned, m)
			have[m.ID] = true
		}
	}
}
