package dream

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
)

// DownscaleFactor is the nightly homeostatic multiplier (the SHY hypothesis:
// sleep globally renormalises weights). Small, so the field re-normalises gently
// and reinforced memories stand out by relative height without anything
// meaningful vanishing between one night and the next.
const DownscaleFactor = 0.98

// SalienceFloor stops downscaling from ever erasing a memory outright.
const SalienceFloor = 0.05

// nrem is the stabilising phase: replay, then homeostatic downscaling. Neither
// step depends on anything observed in the background — both work purely over
// the memory store.
//
// A third step used to sit between these two: gist extraction, which mined
// ambient capture (internal/routine's FindPeriodic/FindSequences) for recurring
// structure and stored the strongest patterns as standing facts. That step was
// cut in 0.3.0 along with the rest of the ambient-capture tier
// (plans/plan0-3-0.md) — it had no data source left. The gap it leaves is
// deliberate and open: genuine multi-memory compression (several corroborating
// facts folded into one denser, higher-confidence one, as opposed to today's
// pairwise memory.Consolidate) belongs here once it exists, targeted for 0.4.0.
func nrem(db *sql.DB, rt *router.Router, dryRun bool, res *Result) error {
	// 1. Prioritised replay — fold near-duplicates and supersede stale facts.
	//    This is the consolidation the store already knows how to do; the dream
	//    is simply when it runs. It mutates, so it is skipped under dry run.
	if !dryRun {
		merged, superseded, err := memory.Consolidate(db, rt)
		switch {
		case err == nil:
			res.Merged, res.Superseded = merged, superseded
			res.Replayed = merged + superseded
		case errors.Is(err, router.ErrNoRuntime), errors.Is(err, router.ErrNoModel):
			// Replay needs a model to judge whether two memories say the same
			// thing. Not having one is a condition, reported as a skip.
			res.ReplaySkipped = true
		default:
			// Anything else is a real failure, and swallowing it printed
			// "0 consolidated" — a night that did nothing, indistinguishable
			// from a night that had nothing to do.
			return fmt.Errorf("replaying memories: %w", err)
		}
	}

	// 2. Homeostatic downscaling — renormalise the whole field.
	n, err := downscale(db, dryRun)
	if err != nil {
		return err
	}
	res.Downscaled = n
	return nil
}

// downscale multiplies every active memory's salience by DownscaleFactor, floored,
// and reports how many rows would actually move. Deliberately *not* logged per
// memory: it touches every row, and a memory_log line each would bury the
// timeline the log exists to keep legible. Only structural events (merge,
// supersede) leave a trace.
func downscale(db *sql.DB, dryRun bool) (int, error) {
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM memories WHERE superseded = 0 AND salience > ?`, SalienceFloor).Scan(&n); err != nil {
		return 0, err
	}
	if dryRun || n == 0 {
		return n, nil
	}
	_, err := db.Exec(
		`UPDATE memories SET salience = MAX(?, salience * ?) WHERE superseded = 0 AND salience > ?`,
		SalienceFloor, DownscaleFactor, SalienceFloor)
	return n, err
}
