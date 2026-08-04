// Package reflection is descriptive statistics over long-term memory.
//
// Where the mirror interprets ("here's a blind spot I notice"), reflection just
// counts: how much the assistant knows and of what kind, how sure it is, how the
// store has grown, what it leans on most, and which commitments have lingered
// longest. It is the plain, numeric floor beneath the interpretive layer — no
// model runs, every figure traces to a row, so it is instant, offline, and
// impossible to argue with. Compute the numbers here; narrate them elsewhere.
package reflection

import (
	"database/sql"
	"sort"
	"time"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/secretary"
)

// growthWeeks is how many trailing weeks of learning the growth series covers.
const growthWeeks = 8

// Count is a labelled tally (a kind, a project).
type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// WeekCount is how many memories were learned in one trailing week.
type WeekCount struct {
	WeekStart int64 `json:"week_start"`
	Learned   int   `json:"learned"`
}

// Reflection is the computed snapshot.
type Reflection struct {
	Total          int                    `json:"total"`
	ByKind         []Count                `json:"by_kind"`
	ByProject      []Count                `json:"by_project"`
	AvgConfidence  float64                `json:"avg_confidence"`
	HighConfidence int                    `json:"high_confidence"` // >= 0.8, things it's sure of
	Hypotheses     int                    `json:"hypotheses"`      // < 0.6, hunches to corroborate
	Growth         []WeekCount            `json:"growth"`          // oldest week first
	MostExercised  []memory.Memory        `json:"most_exercised"`  // recalled most
	LingeringLoops []secretary.Commitment `json:"lingering_loops"` // oldest open first
}

// Compose gathers the reflection as of now.
func Compose(db *sql.DB, now time.Time) (Reflection, error) {
	if err := memory.Init(db); err != nil {
		return Reflection{}, err
	}
	if err := secretary.Init(db); err != nil {
		return Reflection{}, err
	}

	mems, err := memory.All(db)
	if err != nil {
		return Reflection{}, err
	}

	var r Reflection
	r.Total = len(mems)

	kinds := map[string]int{}
	projects := map[string]int{}
	var confSum float64
	for _, m := range mems {
		kinds[string(m.Kind)]++
		if m.Project != "" {
			projects[m.Project]++
		}
		confSum += m.Confidence
		if m.Confidence >= 0.8 {
			r.HighConfidence++
		}
		if m.Confidence < 0.6 {
			r.Hypotheses++
		}
	}
	if r.Total > 0 {
		r.AvgConfidence = confSum / float64(r.Total)
	}
	r.ByKind = topCounts(kinds, 0)       // all kinds
	r.ByProject = topCounts(projects, 6) // busiest projects

	// Growth: memories learned per trailing week, oldest first. Windows are
	// half-open [start, end) — the -1 keeps a memory landing exactly on a week
	// boundary from being counted in both adjacent weeks.
	for w := growthWeeks - 1; w >= 0; w-- {
		end := now.AddDate(0, 0, -7*w)
		start := end.AddDate(0, 0, -7)
		n, _ := memory.LearnedBetween(db, start.Unix(), end.Unix()-1)
		r.Growth = append(r.Growth, WeekCount{WeekStart: start.Unix(), Learned: n})
	}

	// Most exercised: what recall leans on, by use count.
	exercised := append([]memory.Memory(nil), mems...)
	sort.Slice(exercised, func(i, j int) bool { return exercised[i].Uses > exercised[j].Uses })
	for _, m := range exercised {
		if m.Uses == 0 || len(r.MostExercised) >= 5 {
			break
		}
		r.MostExercised = append(r.MostExercised, m)
	}

	// Lingering loops: open commitments, oldest first.
	if open, err := secretary.Open_(db); err == nil {
		sort.Slice(open, func(i, j int) bool { return open[i].Created < open[j].Created })
		if len(open) > 5 {
			open = open[:5]
		}
		r.LingeringLoops = open
	}

	return r, nil
}

// topCounts turns a tally map into a sorted slice, largest first. limit <= 0
// returns all.
func topCounts(m map[string]int, limit int) []Count {
	var out []Count
	for k, n := range m {
		out = append(out, Count{Label: k, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Label < out[j].Label
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
