package procedure

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/deadend"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/textmatch"
)

// A Hit is one recorded procedure bearing on a proposed approach.
type Hit struct {
	Text    string
	Project string
	Agent   string
	When    int64
	Score   float64
	// Elsewhere is true when this was recorded on a different project than
	// the one being asked about — worth surfacing and worth flagging, the
	// same distinction deadend.Ruling draws for the same reason.
	Elsewhere bool
	Record    Record
	// Stale mirrors deadend.Ruling.Stale: a version-bound procedure old enough
	// that the dependency it names may have moved. Surfaced as a caveat,
	// never dropped — a stale procedure is still the best lead available.
	Stale bool
}

// cosineFloor matches deadend's: high on purpose. A checker an agent learns
// to skim past is worse than no checker, and that cost is higher here than on
// the dead-end side — a wrong dead end wastes a glance, a wrong procedure
// sends an agent down a path.
const cosineFloor = 0.74

// Check returns the recorded procedures bearing on a proposed approach, best
// match first, out of the given corpus. The corpus is every memory.Kind =
// Procedure row in the vault — gathering it is the caller's job (see
// memory.RecallProcedures), so this package can be tested against a handful
// of hand-built candidates without a store behind it.
//
// Ranking mirrors deadend.Check exactly: lexical containment for a proposal
// restating a route in the same vocabulary, embedding cosine for the same
// idea in different words, same-project rulings first, then score. Degrades
// to lexical-only with no embedder rather than disappearing.
func Check(corpus []memory.Memory, p *provider.Provider, embedModel, approach, project string, k int) ([]Hit, error) {
	if strings.TrimSpace(approach) == "" || len(corpus) == 0 {
		return nil, nil
	}

	asked := textmatch.Subject(approach)
	lexical := make([]float64, len(corpus))
	records := make([]Record, len(corpus))
	for i, c := range corpus {
		records[i] = ParseRecord(c.Text)
		lexical[i] = textmatch.Overlap(asked, textmatch.Subject(records[i].Route))
	}

	semantic := make([]float64, len(corpus))
	if p != nil {
		texts := make([]string, 0, len(corpus)+1)
		texts = append(texts, approach)
		for i := range corpus {
			texts = append(texts, records[i].Route)
		}
		if vecs, err := p.Embed(embedModel, texts); err == nil && len(vecs) == len(texts) {
			for i := range corpus {
				semantic[i] = cosine(vecs[0], vecs[i+1])
			}
		}
	}

	var hits []Hit
	for i, c := range corpus {
		if lexical[i] < textmatch.Related && semantic[i] < cosineFloor {
			continue
		}
		hits = append(hits, Hit{
			Text: records[i].Route, Project: c.Project, Agent: c.Agent, When: c.Created,
			Score:     math.Max(lexical[i], semantic[i]),
			Elsewhere: project != "" && c.Project != "" && c.Project != project,
			Record:    records[i],
			Stale:     deadend.PossiblySuperseded(records[i].Scope, c.Created, time.Now()),
		})
	}

	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].Elsewhere != hits[b].Elsewhere {
			return !hits[a].Elsewhere
		}
		return hits[a].Score > hits[b].Score
	})
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
