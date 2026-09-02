package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/router"
)

// Testing the full extract→store→recall pipeline, not just retrieval.
//
// The benchmark measures the retrieval backend on ready-made sessions. This
// measures the whole loop the assistant actually runs: read a conversation,
// extract what is worth remembering (a model call), store it, and later recall
// it from a natural question. It reports both correctness — did the fact survive
// the round trip — and efficiency — how long extraction and recall take, and
// whether dedup keeps the store from bloating.

// PipelineCase is one conversation and what should be learnable from it.
type PipelineCase struct {
	Name     string
	Exchange string   // the conversation to learn from
	Probe    string   // a later question that should recall the fact
	Expect   []string // any of these substrings appearing in a recalled memory counts as a hit
}

// PipelineReport is the outcome of a pipeline run.
type PipelineReport struct {
	Cases       int
	Learned     int           // memories stored
	RecallHits  int           // probes that recalled the expected fact
	Duplicates  int           // re-learning the same case stored nothing new (good)
	LearnTime   time.Duration // total extraction time
	RecallTime  time.Duration // total recall time
	MemoryCount int           // final store size
	Details     []string      // per-case notes
}

func (r PipelineReport) Accuracy() float64 {
	if r.Cases == 0 {
		return 0
	}
	return float64(r.RecallHits) / float64(r.Cases)
}

// RunPipeline learns from each case, then probes recall, over a fresh store.
// It also re-learns every case once to confirm dedup holds (no growth).
func RunPipeline(db *sql.DB, rt *router.Router, cases []PipelineCase) (PipelineReport, error) {
	if err := Init(db); err != nil {
		return PipelineReport{}, err
	}
	embed, _ := rt.Model(router.T0)
	rep := PipelineReport{Cases: len(cases)}

	// --- learn ---
	for _, c := range cases {
		t0 := time.Now()
		n, err := Learn(db, rt, c.Exchange, "pipeline-test")
		rep.LearnTime += time.Since(t0)
		if err != nil {
			return rep, fmt.Errorf("learn %q: %w", c.Name, err)
		}
		rep.Learned += n
	}

	beforeReLearn, _ := Count(db)

	// --- re-learn: the same facts a second time must not accumulate ---
	for _, c := range cases {
		if _, err := Learn(db, rt, c.Exchange, "pipeline-test"); err != nil {
			return rep, err
		}
	}
	afterReLearn, _ := Count(db)
	dedupGrowth := afterReLearn - beforeReLearn // want ~0: re-learning adds nothing

	// --- recall ---
	for i, c := range cases {
		t0 := time.Now()
		mems, err := Recall(db, rt.Local(), embed, c.Probe, 5)
		rep.RecallTime += time.Since(t0)
		if err != nil {
			return rep, err
		}
		hit := recallCovers(mems, c.Expect)
		if hit {
			rep.RecallHits++
		}
		mark := "✗"
		if hit {
			mark = "✓"
		}
		rep.Details = append(rep.Details, fmt.Sprintf("%s %s → %s", mark, c.Name, topText(mems)))
		_ = i
	}

	rep.MemoryCount, _ = Count(db)
	rep.Duplicates = dedupGrowth
	return rep, nil
}

// recallCovers reports whether any recalled memory contains one of the expected
// substrings (case-insensitive).
func recallCovers(mems []Memory, expect []string) bool {
	for _, m := range mems {
		low := strings.ToLower(m.Text)
		for _, e := range expect {
			if strings.Contains(low, strings.ToLower(e)) {
				return true
			}
		}
	}
	return false
}

func topText(mems []Memory) string {
	if len(mems) == 0 {
		return "(nothing recalled)"
	}
	t := mems[0].Text
	if len(t) > 60 {
		t = t[:60] + "…"
	}
	return t
}
