package index

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/pragun/brain/internal/provider"
)

// Hit is one retrieved note.
type Hit struct {
	Slug, Title, Kind, Body string
	Score                   float64
	// FirstSeen is the note's date from its frontmatter, unix seconds, or zero
	// when it has none. Carried on the hit rather than looked up later because a
	// caller filtering "what did I do last month" needs it for every candidate,
	// and a per-hit query to answer that would be the retrieval path's slowest
	// step for the sake of one integer already sitting in the row.
	FirstSeen int64
	// Via is set when the note was pulled in as a graph neighbour rather than
	// matched directly, so the UI can show why it is in context.
	Via string
}

// Brute-force cosine is deliberate. At vault scale (a few thousand notes) it is
// well under 5ms, and it keeps the index a plain SQLite file with no extension
// to install. Revisit past ~50k notes.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
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

func (ix *Index) Search(p *provider.Provider, model, query string, k int) ([]Hit, error) {
	vecs, err := p.Embed(model, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned for query")
	}
	q := vecs[0]

	rows, err := ix.DB.Query(`
		SELECT n.slug, n.title, n.kind, n.body, n.first_seen, e.vec
		FROM embeddings e JOIN notes n ON n.slug = e.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		var blob []byte
		if err := rows.Scan(&h.Slug, &h.Title, &h.Kind, &h.Body, &h.FirstSeen, &blob); err != nil {
			return nil, err
		}
		h.Score = cosine(q, blobToFloats(blob))
		hits = append(hits, h)
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Expand pulls in notes linked from the top hits. This is the payoff of keeping
// a graph at all: asking about a project surfaces the people on it even when
// their notes share no vocabulary with the question.
func (ix *Index) Expand(hits []Hit, minConf float64, limit int) ([]Hit, error) {
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		seen[h.Slug] = true
	}

	var out []Hit
	for _, hit := range hits {
		if len(out) >= limit {
			break
		}
		rows, err := ix.DB.Query(`
			SELECT n.slug, n.title, n.kind, n.body, n.first_seen, e.pred
			FROM edges e JOIN notes n ON n.slug = e.obj OR n.slug LIKE '%/' || e.obj
			WHERE e.src_slug = ? AND e.conf >= ?`, hit.Slug, minConf)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h Hit
			var pred string
			if err := rows.Scan(&h.Slug, &h.Title, &h.Kind, &h.Body, &h.FirstSeen, &pred); err != nil {
				rows.Close()
				return nil, err
			}
			if seen[h.Slug] || len(out) >= limit {
				continue
			}
			seen[h.Slug] = true
			h.Score = hit.Score * 0.5
			h.Via = fmt.Sprintf("%s —%s→", hit.Title, pred)
			out = append(out, h)
		}
		rows.Close()
	}
	return out, nil
}

// Ask is retrieval plus generation. Context is capped by character budget
// rather than token count so this stays honest on a 4k-context local model.
func (ix *Index) Ask(p *provider.Provider, embedModel, chatModel, question string, k, budget int) (string, []Hit, error) {
	hits, err := ix.HybridSearch(p, embedModel, question, k)
	if err != nil {
		return "", nil, err
	}
	neighbours, err := ix.Expand(hits, 0.6, k/2)
	if err != nil {
		return "", nil, err
	}
	hits = append(hits, neighbours...)

	var ctx strings.Builder
	for _, h := range hits {
		chunk := fmt.Sprintf("## %s [%s]\n%s\n\n", h.Title, h.Slug, strings.TrimSpace(h.Body))
		if ctx.Len()+len(chunk) > budget {
			break
		}
		ctx.WriteString(chunk)
	}

	if ctx.Len() == 0 {
		return "Nothing in the vault touches that yet.", hits, nil
	}

	const system = "You answer strictly from the provided vault notes. " +
		"Cite the note slug in square brackets after each claim. " +
		"If the notes do not contain the answer, say so plainly rather than guessing."

	answer, err := p.Chat(chatModel, system,
		fmt.Sprintf("Notes:\n\n%s---\n\nQuestion: %s", ctx.String(), question), nil)
	if err != nil {
		return "", hits, err
	}
	return answer, hits, nil
}
