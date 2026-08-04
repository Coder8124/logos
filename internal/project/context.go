package project

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
)

// A context pack is the coherent bundle an external tool needs to work on
// something: the project a file belongs to, its goals and recent progress, the
// people involved, what the assistant knows about it, and the user's standing
// preferences. It turns "here is a file" into "here is everything relevant to
// this file" — assembled locally and handed to Cursor or Claude in one shot,
// instead of re-explained every session. This is the developer-facing face of
// the memory layer.

type ContextPack struct {
	Hint        string          `json:"hint"`
	Project     *Project        `json:"project,omitempty"`
	Preferences []memory.Memory `json:"preferences"`
	Related     []memory.Memory `json:"related"`
}

const (
	maxPrefs   = 8
	maxRelated = 8
	maxListed  = 8
)

// BuildContext resolves a hint — a file path, a project name, or a free topic —
// to a context pack. embed may be nil, in which case the semantically-related
// memories are skipped; the project dossier and standing preferences still work.
//
// Project resolution is best-effort enrichment: if the vault has no project notes
// yet (or the tables aren't built), the pack degrades to standing preferences
// plus related memories rather than failing — a memory layer shouldn't error just
// because a caller asked about an unknown thing.
func BuildContext(db *sql.DB, embed *provider.Provider, embedModel, hint string) (ContextPack, error) {
	pack := ContextPack{Hint: hint}
	pack.Project = resolveProject(db, hint)

	// Standing preferences apply everywhere — exactly what a tool should honour
	// regardless of which file it happens to be touching.
	if prefs, err := memory.Surface(db, []memory.Kind{memory.Preference}, maxPrefs); err == nil {
		for _, m := range prefs {
			if m.Project == "" { // global preferences only
				pack.Preferences = append(pack.Preferences, m)
			}
		}
	}

	// Related memories: semantic recall against the hint, biased by the project.
	if embed != nil {
		q := hint
		if pack.Project != nil {
			q = pack.Project.Name + " " + hint
		}
		if mems, err := memory.Recall(db, embed, embedModel, q, maxRelated); err == nil {
			pack.Related = mems
		}
	}
	return pack, nil
}

// resolveProject maps a hint to a project: by name/slug first, then by a file the
// project has touched (so `context README.md` finds the project that edits it),
// then by the file's parent directory. Returns nil when nothing matches — a topic
// with no project is still a valid pack.
func resolveProject(db *sql.DB, hint string) *Project {
	if p, ok, err := Get(db, hint); err == nil && ok {
		return &p
	}

	projects, err := Detect(db)
	if err != nil {
		return nil // no project layer available: degrade gracefully
	}
	base := strings.ToLower(filepath.Base(hint))
	for _, p := range projects { // most-recently-active first
		for _, f := range p.Files {
			if strings.EqualFold(f.Path, hint) || strings.ToLower(filepath.Base(f.Path)) == base {
				pp := p
				return &pp
			}
		}
	}
	if dir := filepath.Base(filepath.Dir(hint)); dir != "" && dir != "." && dir != string(filepath.Separator) {
		if p, ok, err := Get(db, dir); err == nil && ok {
			return &p
		}
	}
	return nil
}

// Render writes the pack as clean markdown — the format an AI tool ingests.
func (c ContextPack) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Context for %s\n", c.Hint)

	if p := c.Project; p != nil {
		fmt.Fprintf(&b, "\n## Project: %s  (last active %s)\n", p.Name, Age(p.LastActive))

		if len(p.Goals) > 0 {
			b.WriteString("\n**Goals**\n")
			for _, g := range clip(p.Goals, 6) {
				fmt.Fprintf(&b, "- %s\n", oneLine(g))
			}
		}
		if len(p.Progress) > 0 {
			b.WriteString("\n**Recent progress**\n")
			for i, pr := range p.Progress {
				if i >= maxListed {
					break
				}
				fmt.Fprintf(&b, "- %s: %s\n", Age(pr.TS), oneLine(pr.Text))
			}
		}
		if len(p.People) > 0 {
			var names []string
			for _, r := range p.People {
				names = append(names, r.Title)
			}
			fmt.Fprintf(&b, "\n**People**: %s\n", strings.Join(names, ", "))
		}
		if len(p.Files) > 0 {
			b.WriteString("\n**Files**\n")
			for i, f := range p.Files {
				if i >= maxListed {
					break
				}
				fmt.Fprintf(&b, "- %s\n", f.Path)
			}
		}
		if len(p.Memories) > 0 {
			b.WriteString("\n**What the assistant knows about this project**\n")
			for i, m := range p.Memories {
				if i >= maxListed {
					break
				}
				fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, oneLine(m.Text))
			}
		}
	} else {
		fmt.Fprintf(&b, "\n_No project matched %q — standing context only._\n", c.Hint)
	}

	if len(c.Preferences) > 0 {
		b.WriteString("\n## Standing preferences\n")
		for _, m := range c.Preferences {
			fmt.Fprintf(&b, "- %s\n", oneLine(m.Text))
		}
	}
	if len(c.Related) > 0 {
		b.WriteString("\n## Related memories\n")
		for _, m := range c.Related {
			fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, oneLine(m.Text))
		}
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > 100 {
		s = s[:99] + "…"
	}
	return s
}

func clip(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
