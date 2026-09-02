package adapters

import (
	"math"
	"sort"
	"strings"

	"github.com/Coder8124/brain/internal/eval"
	"github.com/Coder8124/brain/internal/provider"
)

// The baselines exist to keep the headline numbers honest.
//
// Without a floor, a suite cannot tell a good memory system from an easy suite:
// if None scores well on a scenario, that scenario is measuring nothing. Without
// a ceiling, recall looks like an achievement when it is really just a budget
// being ignored — Dump wins recall on almost everything by pasting the entire
// history into the window, which is exactly why Density is reported next to it.
//
// StaticFile is the one that matters commercially. It is what people actually
// do today: a CLAUDE.md or .cursorrules that a human remembers to update. Any
// memory system that cannot beat a hand-maintained text file is not worth
// installing.

// ---------------------------------------------------------------------------

// None answers nothing. The floor: whatever it scores, the suite is giving away
// for free.
type None struct{}

func (None) Name() string                           { return "none" }
func (None) Reset() error                           { return nil }
func (None) Write(eval.Event) error                 { return nil }
func (None) Read(eval.Query) (eval.Response, error) { return eval.Response{}, nil }
func (None) Close() error                           { return nil }

// ---------------------------------------------------------------------------

// Dump concatenates the entire history, newest first, until the budget runs
// out. The "just give the model everything" ceiling — and the reason Density is
// a headline metric rather than a footnote.
type Dump struct{ events []eval.Event }

func (d *Dump) Name() string { return "full-dump" }
func (d *Dump) Reset() error { d.events = nil; return nil }
func (d *Dump) Write(ev eval.Event) error {
	d.events = append(d.events, ev)
	return nil
}
func (d *Dump) Close() error { return nil }

func (d *Dump) Read(q eval.Query) (eval.Response, error) {
	// Newest first: if something has to be cut, cut the oldest. This is the most
	// favourable ordering for a dump, so the comparison is against the strong
	// form of the idea.
	ordered := make([]eval.Event, len(d.events))
	copy(ordered, d.events)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].TS > ordered[j].TS })

	var b strings.Builder
	for _, ev := range ordered {
		line := ev.Flatten()
		if q.Budget > 0 && eval.Tokens(b.String())+eval.Tokens(line) > q.Budget {
			break
		}
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	return eval.Response{Text: b.String()}, nil
}

// ---------------------------------------------------------------------------

// Recency keeps the newest events that fit. This is what conversation
// compaction approximates: no notion of relevance, only of lateness.
type Recency struct{ events []eval.Event }

func (r *Recency) Name() string { return "recency-window" }
func (r *Recency) Reset() error { r.events = nil; return nil }
func (r *Recency) Write(ev eval.Event) error {
	r.events = append(r.events, ev)
	return nil
}
func (r *Recency) Close() error { return nil }

func (r *Recency) Read(q eval.Query) (eval.Response, error) {
	ordered := make([]eval.Event, len(r.events))
	copy(ordered, r.events)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].TS > ordered[j].TS })

	budget := q.Budget
	if budget == 0 {
		budget = 2000
	}
	// Half the ceiling, because compaction leaves room for the conversation
	// itself. A window that fills the context with history is not a window.
	budget /= 2

	var kept []string
	used := 0
	for _, ev := range ordered {
		line := ev.Flatten()
		cost := eval.Tokens(line)
		if used+cost > budget && len(kept) > 0 {
			break
		}
		kept = append(kept, line)
		used += cost
	}
	return eval.Response{Text: strings.Join(kept, "\n\n")}, nil
}

// ---------------------------------------------------------------------------

// StaticFile is the CLAUDE.md baseline: a hand-maintained document that a human
// updates when they remember to.
//
// It receives only documents. That is not a handicap the harness invented — it
// is the defining property of the approach. Nobody appends every working note
// and every dead end to a rules file, which is why the file is always current
// on architecture and always silent on what happened yesterday.
//
// It is Durable: a file survives anything, which is precisely its appeal.
type StaticFile struct{ docs []eval.Event }

func (s *StaticFile) Name() string { return "static-file" }
func (s *StaticFile) Reset() error { s.docs = nil; return nil }
func (s *StaticFile) Close() error { return nil }
func (s *StaticFile) Write(ev eval.Event) error {
	if ev.Kind == eval.KindDoc {
		s.docs = append(s.docs, ev)
	}
	return nil
}
func (s *StaticFile) DropDerived() error { return nil } // a file has nothing derived to lose

func (s *StaticFile) Read(q eval.Query) (eval.Response, error) {
	var b strings.Builder
	for _, d := range s.docs {
		line := d.Flatten()
		if q.Budget > 0 && eval.Tokens(b.String())+eval.Tokens(line) > q.Budget {
			break
		}
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	return eval.Response{Text: b.String()}, nil
}

// ---------------------------------------------------------------------------

// VectorRAG is top-k cosine over embedded events, with no lexical arm, no
// graph, and no notion of a checkpoint. It is what most "memory layers" reduce
// to once the marketing is removed, and it is the honest thing to compare
// against — beating None proves nothing; beating this proves something.
type VectorRAG struct {
	p      *provider.Provider
	model  string
	k      int
	events []eval.Event
	vecs   [][]float32
}

func NewVectorRAG(p *provider.Provider, model string, k int) *VectorRAG {
	if k <= 0 {
		k = 8
	}
	return &VectorRAG{p: p, model: model, k: k}
}

func (v *VectorRAG) Name() string { return "vector-rag" }
func (v *VectorRAG) Reset() error { v.events, v.vecs = nil, nil; return nil }
func (v *VectorRAG) Close() error { return nil }

func (v *VectorRAG) Write(ev eval.Event) error {
	text := ev.Flatten()
	vecs, err := v.p.Embed(v.model, []string{text})
	if err != nil {
		return err
	}
	v.events = append(v.events, ev)
	if len(vecs) == 1 {
		v.vecs = append(v.vecs, vecs[0])
	} else {
		v.vecs = append(v.vecs, nil)
	}
	return nil
}

func (v *VectorRAG) Read(q eval.Query) (eval.Response, error) {
	query := q.Task
	if q.Project != "" {
		query = q.Project + ": " + query
	}
	qv, err := v.p.Embed(v.model, []string{query})
	if err != nil || len(qv) == 0 {
		return eval.Response{}, err
	}

	type scored struct {
		i   int
		sim float64
	}
	ranked := make([]scored, 0, len(v.events))
	for i, vec := range v.vecs {
		ranked = append(ranked, scored{i, cosine(qv[0], vec)})
	}
	sort.Slice(ranked, func(a, b int) bool { return ranked[a].sim > ranked[b].sim })

	var b strings.Builder
	for i := 0; i < v.k && i < len(ranked); i++ {
		line := v.events[ranked[i].i].Flatten()
		if q.Budget > 0 && eval.Tokens(b.String())+eval.Tokens(line) > q.Budget {
			break
		}
		b.WriteString(line)
		b.WriteString("\n\n")
	}
	return eval.Response{Text: b.String()}, nil
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
