package graph

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/memory"
)

// The memory layer: what the assistant learned, and the projects work was
// filed under, drawn into the same neighbourhood as the notes. Most of what a
// Logos vault holds is not wikilinked notes but memories and checkpoints, and
// a graph of notes alone drew most vaults as a line or as nothing.
//
// Every node and edge here is read from the index at view time and stored
// nowhere. The memories table is rebuilt from the memory stores and the edges
// from the checkpoints, so deleting the index and running `logos index` draws
// the same picture (invariant 1).

// memoryLayer is the part of the graph the edges table does not hold.
type memoryLayer struct {
	nodes map[string]Node   // memory nodes, by slug
	edges map[string][]Edge // by each endpoint, so the walk finds them from either end
	hubs  map[string]string // project slug -> name, for projects with no note
	db    *sql.DB
}

func memorySlug(id int64) string { return fmt.Sprintf("memory:%d", id) }

// ProjectSlug is where a project sits in the graph: its own note when the
// vault has one, and otherwise a node named for it. The fallback slug is
// "projects/<name>" on purpose — the edges table stores a checkpoint's project
// as the bare name, and the walk already matches an incoming edge by the last
// segment of the slug, so the hub finds its checkpoints with no special case.
func ProjectSlug(db *sql.DB, name string) string {
	if slug, ok := resolveObj(db, name); ok {
		return slug
	}
	return "projects/" + name
}

// hub is ProjectSlug, remembering which slugs it made up so they are drawn as
// a project rather than as a note that has not been written yet.
func (l *memoryLayer) hub(name string) string {
	if slug, ok := resolveObj(l.db, name); ok {
		return slug
	}
	slug := "projects/" + name
	l.hubs[slug] = name
	return slug
}

func (l *memoryLayer) add(e Edge) {
	l.edges[e.Src] = append(l.edges[e.Src], e)
	l.edges[e.Dst] = append(l.edges[e.Dst], e)
}

// loadMemoryLayer reads the memories a user would be shown: not superseded,
// and not quarantined — a quarantined memory is one Logos suspected of
// carrying instructions, and a graph is not the place to promote it back into
// view.
func loadMemoryLayer(db *sql.DB) (memoryLayer, error) {
	l := memoryLayer{nodes: map[string]Node{}, edges: map[string][]Edge{}, hubs: map[string]string{}, db: db}
	if !hasTable(db, "memories") {
		// The table is made by the first memory written. Without it there are
		// no memories to draw, which is a true answer rather than a failure:
		// a vault of checkpoints alone still gets its project hubs.
		return l, nil
	}

	type mem struct {
		id                  int64
		text, kind, project string
		created             int64
	}
	// Read to the end and closed before anything else is asked of the
	// single-connection pool; see Around.
	rows, err := db.Query(`SELECT id, text, kind, project, created FROM memories WHERE superseded = 0 AND quarantined = 0`)
	if err != nil {
		return l, fmt.Errorf("reading memories for the graph: %w", err)
	}
	var mems []mem
	for rows.Next() {
		var m mem
		if err := rows.Scan(&m.id, &m.text, &m.kind, &m.project, &m.created); err != nil {
			rows.Close()
			return l, fmt.Errorf("reading memories for the graph: %w", err)
		}
		mems = append(mems, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, fmt.Errorf("reading memories for the graph: %w", err)
	}

	about := map[string]string{}
	for _, m := range mems {
		slug := memorySlug(m.id)
		l.nodes[slug] = Node{Slug: slug, Title: m.text, Kind: "memory", FirstSeen: m.created}
		if p := strings.TrimSpace(m.project); p != "" {
			about[slug] = l.hub(p)
			l.add(Edge{Src: slug, Dst: about[slug], Pred: "about", Conf: 1, Provenance: Typed})
		}
	}

	// A memory that names a note is joined to it — the same matching, by
	// title and alias, that `logos memory graph` uses, so the two views agree.
	mg, err := memory.BuildGraph(db, false)
	if err != nil {
		return l, fmt.Errorf("matching memories to notes for the graph: %w", err)
	}
	for _, e := range mg.Edges {
		if e.Rel != "mentions" || !strings.HasPrefix(e.Src, "m") {
			continue
		}
		src := "memory:" + strings.TrimPrefix(e.Src, "m")
		if _, ok := l.nodes[src]; !ok {
			continue // superseded or quarantined
		}
		if about[src] == e.Dst {
			continue // a memory naming its own project is already joined to it
		}
		l.add(Edge{Src: src, Dst: e.Dst, Pred: "mentions", Conf: 1, Provenance: Typed})
	}
	return l, nil
}

// Resolve turns what a user typed after `logos graph` into a node: a note's
// slug as given, a note named by its last segment, or a project by name —
// which, in most vaults, is the only way in, because a project is a directory
// of checkpoints and a line in a memory rather than a note.
func Resolve(db *sql.DB, arg string) (string, bool) {
	if strings.TrimSpace(arg) == "" {
		return "", false
	}
	if slug, ok := resolveObj(db, arg); ok {
		return slug, true
	}
	name := strings.TrimPrefix(arg, "projects/")
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM edges WHERE pred = 'checkpoint_of' AND obj = ?`, name).Scan(&n)
	if n == 0 && hasTable(db, "memories") {
		db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND superseded = 0 AND quarantined = 0`, name).Scan(&n)
	}
	if n > 0 {
		return ProjectSlug(db, name), true
	}
	return "", false
}

func hasTable(db *sql.DB, name string) bool {
	var s string
	return db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&s) == nil
}

// Condense readies a neighbourhood for a terminal. Only the keep most recent
// checkpoints are drawn, because a project's whole history around its hub is
// forty dots and no room to name one of them; the rest are counted, and the
// caller says how many so the picture does not pass for the whole project.
// Around a project, "<project> — " is taken off its checkpoints' titles: it is
// the same on every one, and it was costing each label its first eight
// columns.
func Condense(g Graph, keep int) (Graph, int) {
	var cps []string
	for _, n := range g.Nodes {
		if n.Kind == "checkpoint" && n.Slug != g.Focus {
			cps = append(cps, n.Slug)
		}
	}
	// A checkpoint is named for when it was written: newest first.
	sort.Slice(cps, func(i, j int) bool { return trailing(cps[i]) > trailing(cps[j]) })
	drop := map[string]bool{}
	for _, s := range cps[min(keep, len(cps)):] {
		drop[s] = true
	}

	out := Graph{Focus: g.Focus}
	linked := map[string]bool{g.Focus: true}
	for _, e := range g.Edges {
		if drop[e.Src] || drop[e.Dst] {
			continue
		}
		out.Edges = append(out.Edges, e)
		linked[e.Src], linked[e.Dst] = true, true
	}

	prefix := ""
	for _, n := range g.Nodes {
		if n.Slug == g.Focus && n.Kind == "project" {
			prefix = n.Title + " — "
		}
	}
	for _, n := range g.Nodes {
		// A node reached only through a checkpoint that is not drawn would
		// float unattached, which reads as a note with no links.
		if drop[n.Slug] || !linked[n.Slug] {
			continue
		}
		if prefix != "" && n.Kind == "checkpoint" {
			n.Title = strings.TrimPrefix(n.Title, prefix)
		}
		out.Nodes = append(out.Nodes, n)
	}
	return out, len(drop)
}
