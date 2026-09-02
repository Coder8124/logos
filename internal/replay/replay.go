// Package replay is "catch me up since I've been away."
//
// It reads how long you have been gone and composes what changed while you were:
// what the memory learned and dropped, which projects moved, which loops you
// closed and how many still hang, and any connections the nightly dream left for
// review. Pure aggregation over data other subsystems already compute — no model
// runs, so it is instant and offline, the same discipline as the brief and the
// weekly review. Opening brain after two weeks should feel like a briefing, not a
// search.
package replay

import (
	"database/sql"
	"time"

	"github.com/Coder8124/brain/internal/dream"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/project"
	"github.com/Coder8124/brain/internal/secretary"
)

// defaultLookbackDays is the window used on the very first replay, when there is
// no prior "last seen" to measure from.
const defaultLookbackDays = 7

// ProjectMove is a project that saw activity within the window.
type ProjectMove struct {
	Name       string `json:"name"`
	LastActive int64  `json:"last_active"`
}

// Result is the catch-up: everything that changed between Since and Until.
type Result struct {
	Since        int64                  `json:"since"`
	Until        int64                  `json:"until"`
	FirstRun     bool                   `json:"first_run"` // no prior last-seen recorded
	Learned      []memory.DiffEntry     `json:"learned"`
	Dropped      []memory.DiffEntry     `json:"dropped"`
	Corroborated []memory.DiffEntry     `json:"corroborated"`
	Projects     []ProjectMove          `json:"projects"`
	ClosedLoops  []secretary.Commitment `json:"closed_loops"`
	OpenLoops    int                    `json:"open_loops"`
	Insights     int                    `json:"insights"` // dreamed connections awaiting review
}

// Away is how long the user was gone.
func (r Result) Away() time.Duration {
	if r.Until <= r.Since {
		return 0
	}
	return time.Duration(r.Until-r.Since) * time.Second
}

// Empty reports whether nothing at all changed — the caller can then say so
// plainly instead of printing a hollow briefing.
func (r Result) Empty() bool {
	return len(r.Learned)+len(r.Dropped)+len(r.Corroborated)+len(r.Projects)+len(r.ClosedLoops)+r.Insights == 0
}

// Compose gathers the catch-up as of now. It does not advance the last-seen
// marker — that is the caller's decision, so a peek can look without resetting
// the clock.
func Compose(db *sql.DB, now time.Time) (Result, error) {
	if err := Init(db); err != nil {
		return Result{}, err
	}
	if err := memory.Init(db); err != nil {
		return Result{}, err
	}
	if err := secretary.Init(db); err != nil {
		return Result{}, err
	}
	if err := dream.InitQueue(db); err != nil {
		return Result{}, err
	}

	since, ok := LastSeen(db)
	res := Result{Until: now.Unix(), FirstRun: !ok}
	if !ok {
		since = now.AddDate(0, 0, -defaultLookbackDays).Unix()
	}
	res.Since = since

	// What the memory learned, dropped, and firmed up.
	diff, err := memory.Diff(db, "", since, now.Unix())
	if err != nil {
		return res, err
	}
	res.Learned, res.Dropped, res.Corroborated = diff.Added, diff.Removed, diff.Corroborated

	// Which projects moved. Best-effort: if the project layer isn't built, skip
	// it rather than fail the whole catch-up.
	if ps, err := project.Detect(db); err == nil {
		for _, p := range ps {
			if p.LastActive >= since {
				res.Projects = append(res.Projects, ProjectMove{Name: p.Name, LastActive: p.LastActive})
			}
		}
	}

	// Loops closed while away, and how many still hang.
	if closed, err := secretary.ResolvedSince(db, since, now.Unix()); err == nil {
		res.ClosedLoops = closed
	}
	res.OpenLoops, _ = secretary.OpenCount(db)

	// Connections the nightly dream proposed but you haven't reviewed.
	if ins, err := dream.List(db, dream.Pending); err == nil {
		res.Insights = len(ins)
	}

	return res, nil
}
