package dream

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/router"
)

// nrem is the stabilising phase: replay, then a count of what has faded. Neither
// step depends on anything observed in the background — both work purely over
// the memory store.
//
// A third step used to sit between these two: gist extraction, which mined
// ambient capture (internal/routine's FindPeriodic/FindSequences) for recurring
// structure and stored the strongest patterns as standing facts. That step was
// cut in 0.3.0 along with the rest of the ambient-capture tier — it had no
// data source left. The gap it leaves is
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

	// 2. Fading — count what disuse has pushed down, and store nothing. The
	//    pass used to multiply every stored salience by 0.98 in the index
	//    alone; no kind file carried it, so `logos index` put the old values
	//    back and ranking depended on whether the cache had survived (#136).
	//    EffectiveSalience already applies disuse wherever salience is read,
	//    so a night has nothing to write, only something to report.
	n, err := memory.Faded(db, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("counting faded memories: %w", err)
	}
	res.Faded = n
	return nil
}
