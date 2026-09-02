package index

import (
	"strings"

	"github.com/Coder8124/brain/internal/provider"
)

// Hybrid search: lexical FTS5 fused with vector similarity.
//
// Inspired by henrydaum/second-brain (MIT), which does hybrid/lexical/semantic
// retrieval. Pure cosine is good at concepts but blind to exact tokens — a name,
// an error code, an ID that never made it into the embedding's notion of
// "meaning". FTS catches those; fusion keeps the conceptual recall of vectors.
// See CREDITS.md.

const ftsSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    slug UNINDEXED, title, body,
    tokenize = 'porter unicode61'
);
`

// lexical returns slugs ranked by FTS5 relevance (bm25, best first).
func (ix *Index) lexical(query string, k int) ([]string, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := ix.DB.Query(
		`SELECT slug FROM notes_fts WHERE notes_fts MATCH ? ORDER BY bm25(notes_fts) LIMIT ?`,
		match, k)
	if err != nil {
		// A malformed FTS query should degrade to "no lexical hits", never take
		// down the whole search.
		return nil, nil
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			out = append(out, slug)
		}
	}
	return out, nil
}

// LexicalSearch is the FTS arm on its own, as full Hits. It is the fallback for
// a machine with no embedding model: worse than hybrid, but it needs no runtime
// and no vectors, so retrieval still works on a fresh checkout.
func (ix *Index) LexicalSearch(query string, k int) ([]Hit, error) {
	slugs, err := ix.lexical(query, k)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(slugs))
	for rank, slug := range slugs {
		h, ok := ix.HitBySlug(slug)
		if !ok {
			continue
		}
		// bm25 ordering is all we have; synthesise a descending score so callers
		// that sort or threshold on Score behave.
		h.Score = 1.0 / float64(rank+1)
		hits = append(hits, h)
	}
	return hits, nil
}

// ftsQuery turns free text into a safe FTS5 OR-query. FTS5 treats bare
// punctuation as syntax, so we keep only alphanumeric tokens and OR them —
// recall-oriented, since fusion with vectors sorts out precision.
func ftsQuery(q string) string {
	var tokens []string
	for _, f := range strings.Fields(q) {
		var b strings.Builder
		for _, r := range f {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		if b.Len() > 1 {
			tokens = append(tokens, `"`+b.String()+`"`)
		}
	}
	return strings.Join(tokens, " OR ")
}

// HybridSearch fuses vector and lexical rankings with reciprocal rank fusion.
//
// RRF scores each result by 1/(rank+k) summed across both lists, which needs no
// score normalisation between two incomparable scales (cosine distance vs bm25)
// — it only trusts the ordering each method is confident about. k=60 is the
// value from the original RRF paper and is not sensitive.
func (ix *Index) HybridSearch(p *provider.Provider, model, query string, k int) ([]Hit, error) {
	// Over-fetch each arm so fusion has material to work with.
	pool := k * 4
	if pool < 20 {
		pool = 20
	}

	vec, err := ix.Search(p, model, query, pool)
	if err != nil {
		return nil, err
	}
	lex, err := ix.lexical(query, pool)
	if err != nil {
		return nil, err
	}

	const rrfK = 60.0
	score := map[string]float64{}
	info := map[string]Hit{}

	for rank, h := range vec {
		score[h.Slug] += 1.0 / (rrfK + float64(rank))
		info[h.Slug] = h
	}
	for rank, slug := range lex {
		score[slug] += 1.0 / (rrfK + float64(rank))
		if _, ok := info[slug]; !ok {
			// A lexical-only hit has no vector Hit yet; fetch its fields so it
			// can still be returned and cited.
			if h, ok := ix.HitBySlug(slug); ok {
				info[slug] = h
			}
		}
	}

	fused := make([]Hit, 0, len(score))
	for slug, s := range score {
		h := info[slug]
		h.Score = s
		fused = append(fused, h)
	}
	// Descending by fused score; stable on slug for determinism.
	for i := 1; i < len(fused); i++ {
		for j := i; j > 0 && (fused[j].Score > fused[j-1].Score ||
			(fused[j].Score == fused[j-1].Score && fused[j].Slug < fused[j-1].Slug)); j-- {
			fused[j], fused[j-1] = fused[j-1], fused[j]
		}
	}
	if len(fused) > k {
		fused = fused[:k]
	}
	return fused, nil
}

func (ix *Index) HitBySlug(slug string) (Hit, bool) {
	var h Hit
	err := ix.DB.QueryRow("SELECT slug, title, kind, body, first_seen FROM notes WHERE slug = ?", slug).
		Scan(&h.Slug, &h.Title, &h.Kind, &h.Body, &h.FirstSeen)
	return h, err == nil
}
