package dream

import (
	"database/sql"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
)

// Phase selects which half of the night to run. NREM stabilises; REM recombines.
// REM always runs after NREM so it works over the cleaned, compressed store.
const (
	PhaseNREM = "nrem"
	PhaseREM  = "rem"
	PhaseAll  = "all"
)

// Result is what a night's sleep did (or, under dry run, would do).
type Result struct {
	Date       string
	Replayed   int  // memories re-affirmed by consolidation (merged + superseded)
	Merged     int  // near-duplicates folded
	Superseded int  // stale facts replaced
	Downscaled int  // memories touched by the homeostatic pass
	Insights   int  // REM connections proposed for review
	REMSkipped bool // REM could not run (no reasoning model)
	// ReplaySkipped is true when consolidation could not run because no model
	// was reachable. Distinct from "nothing to consolidate", which is what a
	// swallowed error used to look like.
	ReplaySkipped bool
}

// Run executes the consolidation pass for one day. NREM's structural edits are
// deterministic maintenance and run headless; REM's inferences become Insights
// in the review queue. Under dryRun nothing is written — the Result reports what
// the pass would change, so a night of sleep is auditable before it is trusted.
//
// date and embedModel are accepted for compatibility with a per-night result
// (the Date field) and were formerly also used to scope gist extraction over a
// window of captured events; that step was cut in 0.3.0
// along with the rest of ambient capture, so both nrem and rem now work purely
// over the memory store.
func Run(db *sql.DB, vaultDir string, rt *router.Router, date time.Time, phase string, dryRun bool) (Result, error) {
	res := Result{Date: date.Format("2006-01-02")}

	if err := memory.Init(db); err != nil {
		return res, err
	}
	if err := InitQueue(db); err != nil {
		return res, err
	}

	if phase == PhaseNREM || phase == PhaseAll {
		if err := nrem(db, rt, dryRun, &res); err != nil {
			return res, err
		}
	}
	if phase == PhaseREM || phase == PhaseAll {
		if err := rem(db, rt, dryRun, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}
