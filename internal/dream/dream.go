package dream

import (
	"database/sql"
	"time"

	"github.com/pragun/brain/internal/capture"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/router"
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
	Gists      int  // standing facts learned from recurring structure
	Downscaled int  // memories touched by the homeostatic pass
	Linked     int  // artifacts tied to their work
	Insights   int  // REM connections proposed for review
	REMSkipped bool // REM could not run (no reasoning model)
}

// Run executes the consolidation pass for one day. NREM's structural edits are
// deterministic maintenance and run headless; REM's inferences become Insights
// in the review queue. Under dryRun nothing is written — the Result reports what
// the pass would change, so a night of sleep is auditable before it is trusted.
func Run(db *sql.DB, vaultDir string, rt *router.Router, embedModel string, date time.Time, phase string, dryRun bool) (Result, error) {
	res := Result{Date: date.Format("2006-01-02")}

	if err := memory.Init(db); err != nil {
		return res, err
	}
	if err := InitQueue(db); err != nil {
		return res, err
	}

	// The day that just ended, in local time.
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	events, err := capture.Range(db, start.Unix(), start.AddDate(0, 0, 1).Unix())
	if err != nil {
		return res, err
	}

	if phase == PhaseNREM || phase == PhaseAll {
		if err := nrem(db, rt, embedModel, events, date, dryRun, &res); err != nil {
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
