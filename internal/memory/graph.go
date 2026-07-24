package memory

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// The memory relationship graph. The note graph (internal/graph) shows how your
// written notes link; this shows how what the assistant has *learned* connects —
// to itself and to the vault. Three kinds of edge:
//
//   - mentions:   a memory names a person/project/topic that is a note (.md
//     file), tying the learned layer to the written one.
//   - similar:    two memories are close in meaning (embedding proximity),
//     surfacing clusters of related knowledge.
//   - supersedes: a memory replaced an older one (from superseded_by), so the
//     graph carries the same history the timeline does.
//
// It is built entirely from the shared index DB. Following the single-connection
// rule, every table is read to completion and its cursor closed before the next
// query runs — no nested cursors.

// GraphNode is a memory or a note in the memory graph.
type GraphNode struct {
	ID         string  `json:"id"`   // "m<id>" for a memory, the note slug for a note
	Type       string  `json:"type"` // "memory" | "note"
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence,omitempty"`
	Salience   float64 `json:"salience,omitempty"`
	Project    string  `json:"project,omitempty"`
	Superseded bool    `json:"superseded,omitempty"`
	Degree     int     `json:"degree"`
}

// GraphEdge connects two nodes.
type GraphEdge struct {
	Src    string  `json:"src"`
	Dst    string  `json:"dst"`
	Rel    string  `json:"rel"` // mentions | similar | supersedes
	Weight float64 `json:"weight"`
}

// MemGraph is the whole thing.
type MemGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// MemSimilarityThreshold gates "similar" edges. High enough that the graph shows
// genuine clusters, not a cobweb.
const MemSimilarityThreshold = 0.60

// BuildGraph assembles the memory graph. withSimilarity adds the (view-time,
// never-stored) similarity edges; they are the densest, so they are opt-in.
func BuildGraph(db *sql.DB, withSimilarity bool) (MemGraph, error) {
	// 1. Load every memory (active and superseded), fully, before any other query.
	type mem struct {
		id           int64
		text, kind   string
		confidence   float64
		salience     float64
		project      string
		superseded   bool
		supersededBy int64
		vec          []float32
	}
	rows, err := db.Query(`SELECT id, text, kind, salience, confidence, project, superseded, superseded_by, vec FROM memories`)
	if err != nil {
		return MemGraph{}, err
	}
	var mems []mem
	for rows.Next() {
		var m mem
		var sup int
		var blob []byte
		if err := rows.Scan(&m.id, &m.text, &m.kind, &m.salience, &m.confidence, &m.project, &sup, &m.supersededBy, &blob); err != nil {
			rows.Close()
			return MemGraph{}, err
		}
		m.superseded = sup != 0
		m.vec = blobToFloats(blob)
		mems = append(mems, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MemGraph{}, err
	}

	// 2. Load note terms (title + aliases) so memories can be linked to .md files.
	terms, err := noteTerms(db)
	if err != nil {
		return MemGraph{}, err
	}

	g := MemGraph{}
	nodeIx := map[string]int{} // node id -> index in g.Nodes
	addNode := func(n GraphNode) {
		if _, ok := nodeIx[n.ID]; ok {
			return
		}
		nodeIx[n.ID] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
	}
	degree := map[string]int{}
	addEdge := func(src, dst, rel string, w float64) {
		g.Edges = append(g.Edges, GraphEdge{Src: src, Dst: dst, Rel: rel, Weight: w})
		degree[src]++
		degree[dst]++
	}

	memID := func(id int64) string { return fmt.Sprintf("m%d", id) }

	// 3. Memory nodes.
	for _, m := range mems {
		addNode(GraphNode{
			ID: memID(m.id), Type: "memory", Label: m.text, Kind: m.kind,
			Confidence: m.confidence, Salience: m.salience, Project: m.project, Superseded: m.superseded,
		})
	}

	// 4. mentions edges: memory -> note whose title/alias appears in the text.
	for _, m := range mems {
		lower := " " + strings.ToLower(m.text) + " "
		linked := map[string]bool{}
		for _, tm := range terms {
			if linked[tm.slug] {
				continue
			}
			if containsTerm(lower, tm.term) {
				addNode(GraphNode{ID: tm.slug, Type: "note", Label: tm.title, Kind: tm.kind})
				addEdge(memID(m.id), tm.slug, "mentions", 1)
				linked[tm.slug] = true
			}
		}
	}

	// 5. supersedes edges: newer -> older (only when both are nodes).
	for _, m := range mems {
		if m.supersededBy != 0 {
			if _, ok := nodeIx[memID(m.supersededBy)]; ok {
				addEdge(memID(m.supersededBy), memID(m.id), "supersedes", 1)
			}
		}
	}

	// 6. similar edges among active memories (opt-in; the densest layer).
	if withSimilarity {
		for i := 0; i < len(mems); i++ {
			if mems[i].superseded || len(mems[i].vec) == 0 {
				continue
			}
			for j := i + 1; j < len(mems); j++ {
				if mems[j].superseded || len(mems[j].vec) == 0 {
					continue
				}
				if sim := cosine(mems[i].vec, mems[j].vec); sim >= MemSimilarityThreshold {
					addEdge(memID(mems[i].id), memID(mems[j].id), "similar", sim)
				}
			}
		}
	}

	for i := range g.Nodes {
		g.Nodes[i].Degree = degree[g.Nodes[i].ID]
	}
	// Deterministic order for stable rendering.
	sort.Slice(g.Nodes, func(a, b int) bool { return g.Nodes[a].ID < g.Nodes[b].ID })
	sort.Slice(g.Edges, func(a, b int) bool {
		if g.Edges[a].Src != g.Edges[b].Src {
			return g.Edges[a].Src < g.Edges[b].Src
		}
		return g.Edges[a].Dst < g.Edges[b].Dst
	})
	return g, nil
}

type noteTerm struct {
	slug, title, kind, term string
}

// noteTerms returns the searchable terms (title and aliases) for every note,
// each tagged with its slug/kind. Read fully, cursors closed, no nesting.
func noteTerms(db *sql.DB) ([]noteTerm, error) {
	// notes table may not exist in a memory-only DB (e.g. the MCP server's view);
	// treat that as "no notes to link" rather than an error.
	rows, err := db.Query(`SELECT slug, title, kind FROM notes`)
	if err != nil {
		return nil, nil
	}
	type note struct{ slug, title, kind string }
	var notes []note
	for rows.Next() {
		var n note
		if rows.Scan(&n.slug, &n.title, &n.kind) == nil {
			notes = append(notes, n)
		}
	}
	rows.Close()

	aliasBySlug := map[string][]string{}
	if arows, err := db.Query(`SELECT slug, alias FROM aliases`); err == nil {
		for arows.Next() {
			var slug, alias string
			if arows.Scan(&slug, &alias) == nil {
				aliasBySlug[slug] = append(aliasBySlug[slug], alias)
			}
		}
		arows.Close()
	}

	var out []noteTerm
	for _, n := range notes {
		title := n.title
		if title == "" {
			title = trailingSeg(n.slug)
		}
		out = append(out, noteTerm{n.slug, title, n.kind, strings.ToLower(title)})
		for _, a := range aliasBySlug[n.slug] {
			out = append(out, noteTerm{n.slug, title, n.kind, strings.ToLower(a)})
		}
	}
	return out, nil
}

// containsTerm reports whether term appears in text as a whole word. text is
// expected pre-lowercased and space-padded; term is lowercased. Terms under 3
// chars are skipped — they match too much to be meaningful links.
func containsTerm(paddedLowerText, term string) bool {
	if len(term) < 3 {
		return false
	}
	i := strings.Index(paddedLowerText, term)
	for i >= 1 {
		before := paddedLowerText[i-1]
		afterIx := i + len(term)
		after := paddedLowerText[afterIx]
		if !isWordByte(before) && !isWordByte(after) {
			return true
		}
		next := strings.Index(paddedLowerText[i+1:], term)
		if next < 0 {
			return false
		}
		i = i + 1 + next
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

func trailingSeg(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[i+1:]
	}
	return slug
}

// Mermaid renders the graph as a Mermaid flowchart — a portable way to actually
// see it (terminal tools, markdown, the artifact viewer) without the widget.
func (g MemGraph) Mermaid() string {
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, n := range g.Nodes {
		label := sanitizeLabel(n.Label)
		id := mermaidID(n.ID)
		if n.Type == "note" {
			fmt.Fprintf(&b, "  %s[[%s]]\n", id, label) // notes as subroutine shape
		} else if n.Superseded {
			fmt.Fprintf(&b, "  %s(%s):::gone\n", id, label)
		} else {
			fmt.Fprintf(&b, "  %s(%s)\n", id, label)
		}
	}
	for _, e := range g.Edges {
		arrow := "-->"
		switch e.Rel {
		case "similar":
			arrow = "-.->" // dotted: a lens, not a fact
		case "supersedes":
			arrow = "==>"
		}
		fmt.Fprintf(&b, "  %s %s|%s| %s\n", mermaidID(e.Src), arrow, e.Rel, mermaidID(e.Dst))
	}
	b.WriteString("  classDef gone stroke-dasharray:4,opacity:0.5\n")
	return b.String()
}

func mermaidID(id string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ".", "_", " ", "_")
	return "n_" + r.Replace(id)
}

func sanitizeLabel(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\"", "'")
	if len(s) > 48 {
		s = s[:47] + "…"
	}
	// Mermaid node text in ()/[] dislikes some chars; quote defensively.
	return "\"" + s + "\""
}
