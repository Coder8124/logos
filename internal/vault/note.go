// Package vault reads and writes notes in the Obsidian vault.
//
// The markdown file is the source of truth. Reading is forgiving — anything it
// cannot parse is preserved by simply not touching it. Writing is rare and
// deliberate: see WriteAtomic, the single door through which anything enters
// the vault.
package vault

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EdgeSrc records where an edge came from, which determines how much we trust it.
type EdgeSrc string

const (
	// Stated: you wrote it — a [[wikilink]] or an explicit frontmatter relation.
	Stated EdgeSrc = "stated"
	// Inferred: a model proposed it and you accepted it.
	Inferred EdgeSrc = "inferred"
	// Imported: pulled in from an external system.
	Imported EdgeSrc = "imported"
)

func parseEdgeSrc(s string) EdgeSrc {
	switch s {
	case "inferred":
		return Inferred
	case "imported":
		return Imported
	default:
		return Stated
	}
}

type Edge struct {
	Pred string  // predicate, e.g. works_on; body wikilinks use "mentions"
	Obj  string  // target note slug, [[ ]] stripped
	Conf float64 // 0..1
	Src  EdgeSrc
}

type Note struct {
	Slug    string
	Path    string
	Title   string
	Kind    string
	Aliases []string
	Body    string
	Edges   []Edge
	Hash    string // content hash, so reindexing only re-embeds what changed
	// FirstSeen is a unix timestamp parsed from frontmatter, 0 if absent. It is
	// what the graph's time scrubber animates over: when each note entered the
	// vault.
	FirstSeen int64
}

type frontmatter struct {
	Type      string        `yaml:"type"`
	Title     string        `yaml:"title"`
	Aliases   []string      `yaml:"aliases"`
	Relations []rawRelation `yaml:"relations"`
	// Any of these date fields seeds first_seen — the rollup writes first_seen,
	// daily notes carry date, recordings carry captured.
	FirstSeen string `yaml:"first_seen"`
	Date      string `yaml:"date"`
	Captured  string `yaml:"captured"`
}

type rawRelation struct {
	Pred string   `yaml:"pred"`
	Obj  string   `yaml:"obj"`
	Conf *float64 `yaml:"conf"`
	Src  string   `yaml:"src"`
}

// [[target]] or [[target|display]] or [[target#heading]]
var wikilinkRe = regexp.MustCompile(`\[\[([^\]\|#]+)(?:[#\|][^\]]*)?\]\]`)

// Slug turns vault/people/Sameer Rao.md into people/sameer-rao.
func Slug(vaultDir, path string) string {
	rel, err := filepath.Rel(vaultDir, path)
	if err != nil {
		rel = path
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	rel = strings.ToLower(rel)
	rel = strings.NewReplacer("_", "-", " ", "-", `\`, "/").Replace(rel)
	return rel
}

// NormalizeLink reduces a wikilink target to its trailing segment. Full entity
// resolution lands in step 3; this is the cheap normalisation that makes
// [[Sameer Rao]] and [[people/sameer-rao]] agree.
func NormalizeLink(target string) string {
	t := strings.ToLower(strings.TrimSpace(target))
	t = strings.NewReplacer("_", "-", " ", "-").Replace(t)
	if i := strings.LastIndex(t, "/"); i >= 0 {
		t = t[i+1:]
	}
	return t
}

func hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum64())
}

// splitFrontmatter splits "---\nyaml\n---\nbody" into its two halves.
func splitFrontmatter(raw string) (string, string) {
	if !strings.HasPrefix(raw, "---\n") {
		return "", raw
	}
	rest := raw[4:]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return "", raw
	}
	body := strings.TrimLeft(rest[i+4:], "\n")
	return rest[:i], body
}

// Parse reads one note. Malformed frontmatter degrades to "no metadata" rather
// than dropping the note: hand-edited vaults will contain broken YAML, and
// silently losing notes is the worst available failure.
func Parse(vaultDir, path, raw string) Note {
	fmStr, body := splitFrontmatter(raw)

	var fm frontmatter
	if fmStr != "" {
		_ = yaml.Unmarshal([]byte(fmStr), &fm)
	}

	slug := Slug(vaultDir, path)

	edges := make([]Edge, 0, len(fm.Relations))
	for _, r := range fm.Relations {
		conf := 1.0
		if r.Conf != nil {
			conf = max(0, min(1, *r.Conf))
		}
		edges = append(edges, Edge{
			Pred: r.Pred,
			Obj:  NormalizeLink(strings.Trim(r.Obj, "[]")),
			Conf: conf,
			Src:  parseEdgeSrc(r.Src),
		})
	}

	// Body wikilinks are things you typed, so they are stated at full
	// confidence with an untyped predicate. A typed relation already covering
	// the same target wins — prose must not downgrade an explicit edge.
	for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
		obj := NormalizeLink(m[1])
		if obj == "" {
			continue
		}
		dup := false
		for _, e := range edges {
			if e.Obj == obj {
				dup = true
				break
			}
		}
		if !dup {
			edges = append(edges, Edge{Pred: "mentions", Obj: obj, Conf: 1.0, Src: Stated})
		}
	}

	title := fm.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	kind := fm.Type
	if kind == "" {
		kind = "note"
	}

	return Note{
		Slug:      slug,
		Path:      path,
		Title:     title,
		Kind:      kind,
		Aliases:   fm.Aliases,
		Body:      body,
		Edges:     edges,
		Hash:      hash(raw),
		FirstSeen: parseDate(fm.FirstSeen, fm.Captured, fm.Date),
	}
}

// parseDate returns the first parseable date among the candidates as a unix
// timestamp, or 0. Accepts plain YYYY-MM-DD (what the vault writes) and RFC3339.
func parseDate(candidates ...string) int64 {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04"} {
			if t, err := time.Parse(layout, c); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

// EmbedText is what actually gets embedded. Title and kind are prepended so a
// query like "who is sameer" can match an otherwise sparse stub note.
func (n Note) EmbedText() string {
	return fmt.Sprintf("%s (%s)\n\n%s", n.Title, n.Kind, n.Body)
}
