// Package graph builds the memory graph the widget renders.
//
// It never returns the whole vault. The graph view is ego-mode only — a focus
// node and its neighbourhood out to a few hops — because the full-graph hairball
// is a screenshot, not a tool: impressive once, useless every time after. The
// backend's job is to extract that neighbourhood, tag each edge with where it
// came from (so the UI can render trust), and, on request, add the view-time
// similarity edges that are computed from vectors already in the index and
// never written to disk.
package graph

import (
	"database/sql"
	"math"
	"sort"
	"strings"
)

// Provenance is where an edge came from — the thing that decides how much a
// viewer should trust it, and therefore how it is drawn.
type Provenance string

const (
	// Wikilink: a [[link]] you typed. Absolute; drawn solid at full opacity.
	Wikilink Provenance = "wikilink"
	// Typed: a frontmatter relation carrying an explicit confidence.
	Typed Provenance = "typed"
	// Similarity: embedding proximity, computed at view time. A lens, not a
	// fact — faint, off by default, never persisted.
	Similarity Provenance = "similarity"
)

type Node struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Degree    int    `json:"degree"`
	FirstSeen int64  `json:"first_seen"`
	// Hops is the distance from the focus node, 0 for the focus itself. The UI
	// dims outer rings so the eye starts at the centre.
	Hops int `json:"hops"`
}

type Edge struct {
	Src        string     `json:"src"`
	Dst        string     `json:"dst"`
	Pred       string     `json:"pred"`
	Conf       float64    `json:"conf"`
	Provenance Provenance `json:"provenance"`
}

type Graph struct {
	Focus string `json:"focus"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// resolveObj turns an edge's normalised target ("logos") into the actual note
// slug ("projects/logos"). Edges store the trailing segment; this matches it
// back the same way retrieval does.
func resolveObj(db *sql.DB, obj string) (string, bool) {
	var slug string
	err := db.QueryRow(
		`SELECT slug FROM notes WHERE slug = ?1 OR slug LIKE '%/' || ?1 LIMIT 1`, obj).Scan(&slug)
	return slug, err == nil
}

// Ego returns the neighbourhood within `hops` of focus. Both directions of each
// edge are followed — a note you are linked *from* is as much a neighbour as one
// you link *to*.
func Ego(db *sql.DB, focus string, hops int, withSimilarity bool) (Graph, error) {
	if hops < 1 {
		hops = 1
	}
	g := Graph{Focus: focus}

	// BFS outward, recording the hop distance the first time each node is seen.
	dist := map[string]int{focus: 0}
	frontier := []string{focus}

	edgeSet := map[string]Edge{}
	edgeKey := func(a, b, pred string) string { return a + "\x00" + b + "\x00" + pred }

	// rawEdge is a row read before it is resolved. We collect rows fully and
	// close the cursor before issuing any follow-up query, because the index
	// runs on a single-connection pool: a nested query while a cursor is open
	// would wait forever for the connection the cursor holds.
	type rawOut struct {
		pred, obj, src string
		conf           float64
	}

	for h := 0; h < hops; h++ {
		var next []string
		for _, cur := range frontier {
			// --- outgoing: cur -> obj ---
			var outs []rawOut
			rows, err := db.Query(`SELECT pred, obj, conf, src FROM edges WHERE src_slug = ?`, cur)
			if err != nil {
				return g, err
			}
			for rows.Next() {
				var r rawOut
				if rows.Scan(&r.pred, &r.obj, &r.conf, &r.src) == nil {
					outs = append(outs, r)
				}
			}
			rows.Close()

			for _, r := range outs {
				dst, ok := resolveObj(db, r.obj)
				if !ok || dst == cur {
					continue
				}
				edgeSet[edgeKey(cur, dst, r.pred)] = Edge{
					Src: cur, Dst: dst, Pred: r.pred, Conf: r.conf, Provenance: provenanceOf(r.pred, r.src, r.conf),
				}
				if _, seen := dist[dst]; !seen {
					dist[dst] = h + 1
					next = append(next, dst)
				}
			}

			// --- incoming: someone -> cur ---
			rows, err = db.Query(
				`SELECT src_slug, pred, conf, src FROM edges WHERE obj = ? OR ? LIKE '%/' || obj`,
				trailing(cur), cur)
			if err != nil {
				return g, err
			}
			type rawIn struct {
				srcSlug, pred, src string
				conf               float64
			}
			var ins []rawIn
			for rows.Next() {
				var r rawIn
				if rows.Scan(&r.srcSlug, &r.pred, &r.conf, &r.src) == nil {
					ins = append(ins, r)
				}
			}
			rows.Close()

			for _, r := range ins {
				if r.srcSlug == cur {
					continue
				}
				edgeSet[edgeKey(r.srcSlug, cur, r.pred)] = Edge{
					Src: r.srcSlug, Dst: cur, Pred: r.pred, Conf: r.conf, Provenance: provenanceOf(r.pred, r.src, r.conf),
				}
				if _, seen := dist[r.srcSlug]; !seen {
					dist[r.srcSlug] = h + 1
					next = append(next, r.srcSlug)
				}
			}
		}
		frontier = next
	}

	// Keep only edges whose endpoints are both in the neighbourhood.
	degree := map[string]int{}
	for _, e := range edgeSet {
		if _, ok := dist[e.Src]; !ok {
			continue
		}
		if _, ok := dist[e.Dst]; !ok {
			continue
		}
		g.Edges = append(g.Edges, e)
		degree[e.Src]++
		degree[e.Dst]++
	}

	// Hydrate nodes with their metadata.
	for slug, h := range dist {
		var title, kind string
		var firstSeen int64
		err := db.QueryRow(`SELECT title, kind, first_seen FROM notes WHERE slug = ?`, slug).
			Scan(&title, &kind, &firstSeen)
		if err != nil {
			// A linked-to note that does not exist yet is a real thing in a
			// wikilink vault; show it as a ghost node rather than dropping the edge.
			title, kind = trailing(slug), "missing"
		}
		g.Nodes = append(g.Nodes, Node{
			Slug: slug, Title: title, Kind: kind, Degree: degree[slug], FirstSeen: firstSeen, Hops: h,
		})
	}

	if withSimilarity {
		g.addSimilarity(db)
	}

	// Deterministic order so the layout is stable across reopens.
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Slug < g.Nodes[j].Slug })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].Src != g.Edges[j].Src {
			return g.Edges[i].Src < g.Edges[j].Src
		}
		return g.Edges[i].Dst < g.Edges[j].Dst
	})
	return g, nil
}

// provenanceOf classifies an edge for rendering. A body wikilink is stored as a
// "mentions" predicate you stated; anything else with a confidence is a typed
// relation.
func provenanceOf(pred, src string, conf float64) Provenance {
	if pred == "mentions" && src == "stated" {
		return Wikilink
	}
	return Typed
}

func trailing(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[i+1:]
	}
	return slug
}

// SimilarityThreshold is how close two notes must be to draw a lens edge between
// them. High on purpose — similarity edges are suggestive, and a low bar turns
// the graph into a cobweb where everything faintly touches everything.
const SimilarityThreshold = 0.62

// addSimilarity adds view-time edges between neighbourhood nodes whose
// embeddings are close. Computed here, never stored: writing a guess to disk
// would launder it into a claim.
func (g *Graph) addSimilarity(db *sql.DB) {
	type vec struct {
		slug string
		v    []float32
	}
	var vecs []vec
	for _, n := range g.Nodes {
		blob, err := embeddingOf(db, n.Slug)
		if err == nil && len(blob) > 0 {
			vecs = append(vecs, vec{n.Slug, blob})
		}
	}

	existing := map[string]bool{}
	for _, e := range g.Edges {
		existing[e.Src+"\x00"+e.Dst] = true
		existing[e.Dst+"\x00"+e.Src] = true
	}

	for i := 0; i < len(vecs); i++ {
		for j := i + 1; j < len(vecs); j++ {
			if existing[vecs[i].slug+"\x00"+vecs[j].slug] {
				continue // already connected by a real edge; no need to suggest it
			}
			sim := cosine(vecs[i].v, vecs[j].v)
			if sim >= SimilarityThreshold {
				g.Edges = append(g.Edges, Edge{
					Src: vecs[i].slug, Dst: vecs[j].slug, Pred: "similar",
					Conf: sim, Provenance: Similarity,
				})
			}
		}
	}
}

func embeddingOf(db *sql.DB, slug string) ([]float32, error) {
	var blob []byte
	if err := db.QueryRow(`SELECT vec FROM embeddings WHERE slug = ?`, slug).Scan(&blob); err != nil {
		return nil, err
	}
	return blobToFloats(blob), nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func blobToFloats(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		bits := uint32(b[4*i]) | uint32(b[4*i+1])<<8 | uint32(b[4*i+2])<<16 | uint32(b[4*i+3])<<24
		v[i] = math.Float32frombits(bits)
	}
	return v
}

// DefaultFocus picks where to open the graph: today's daily note if it exists,
// otherwise the highest-degree node — the natural centre of what you know.
func DefaultFocus(db *sql.DB, todaySlug string) string {
	var slug string
	if db.QueryRow(`SELECT slug FROM notes WHERE slug = ?`, todaySlug).Scan(&slug) == nil {
		return slug
	}
	// Most-connected node: the hub is the most useful place to land.
	row := db.QueryRow(`
		SELECT n.slug FROM notes n
		LEFT JOIN (
			SELECT src_slug AS s FROM edges UNION ALL SELECT obj AS s FROM edges
		) e ON e.s = n.slug OR n.slug LIKE '%/' || e.s
		GROUP BY n.slug ORDER BY COUNT(e.s) DESC LIMIT 1`)
	if row.Scan(&slug) == nil {
		return slug
	}
	return todaySlug
}

// Find looks up a node by fuzzy title/slug match, for the "/" jump-to box.
func Find(db *sql.DB, query string) (string, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return "", false
	}
	rows, err := db.Query(`SELECT slug, title FROM notes`)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var best string
	for rows.Next() {
		var slug, title string
		if rows.Scan(&slug, &title) != nil {
			continue
		}
		if strings.Contains(strings.ToLower(slug), q) || strings.Contains(strings.ToLower(title), q) {
			if best == "" || len(slug) < len(best) {
				best = slug
			}
		}
	}
	return best, best != ""
}
