package dream

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/pragun/brain/internal/capture"
	"github.com/pragun/brain/internal/event"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/routine"
)

// GistWindowDays is how far back gist extraction looks for recurring structure.
// A single day cannot establish a routine; a month is enough to see one form
// without drowning in history.
const GistWindowDays = 30

// DownscaleFactor is the nightly homeostatic multiplier (the SHY hypothesis:
// sleep globally renormalises weights). Small, so the field re-normalises gently
// and reinforced memories stand out by relative height without anything
// meaningful vanishing between one night and the next.
const DownscaleFactor = 0.98

// SalienceFloor stops downscaling from ever erasing a memory outright.
const SalienceFloor = 0.05

// maxGists caps how many standing facts one night may learn. The store should
// pick up habits, not every faint regularity.
const maxGists = 3

// nrem is the stabilising phase: replay, gist extraction, homeostatic
// downscaling, artifact association. Three of the four touch no model at all.
func nrem(db *sql.DB, rt *router.Router, embedModel string, events []event.Event, date time.Time, dryRun bool, res *Result) error {
	// 1. Prioritised replay — fold near-duplicates and supersede stale facts.
	//    This is the consolidation the store already knows how to do; the dream
	//    is simply when it runs. It mutates, so it is skipped under dry run.
	if !dryRun {
		merged, superseded, err := memory.Consolidate(db, rt)
		if err == nil {
			res.Merged, res.Superseded = merged, superseded
			res.Replayed = merged + superseded
		}
	}

	// 2. Gist extraction — turn recurring specifics into standing semantic facts.
	//    Arithmetic path only: patterns that clear routine mining's support
	//    thresholds are counts, not guesses, so they store directly at the "dream"
	//    confidence and dedup against what's already known. (Model-abstracted
	//    gist — collapsing several memories into one preference — is a follow-up
	//    that must go through the same review path as REM, not store directly.)
	gists, err := gistsFromRoutines(db, date)
	if err != nil {
		return err
	}
	p := rt.Local()
	for _, g := range gists {
		if dryRun {
			res.Gists++
			continue
		}
		m := &memory.Memory{Text: g, Kind: memory.Context, Salience: 0.4, Source: "dream"}
		if r, _ := memory.Store(db, p, embedModel, m); r.Created() {
			res.Gists++
		}
	}

	// 3. Homeostatic downscaling — renormalise the whole field.
	n, err := downscale(db, dryRun)
	if err != nil {
		return err
	}
	res.Downscaled = n

	// 4. Artifact association — the files, commits, and pages that mattered today,
	//    tied to the work they belong to. Emitting the graph edges (and the
	//    artifact→conversation links) waits on the graph write API and persisted
	//    conversations; for now this counts what a full pass would wire up.
	res.Linked = countArtifacts(events)
	return nil
}

// gistsFromRoutines mines a recent window for stable structure and phrases the
// strongest patterns as standing facts. Selection is pure arithmetic — the model
// is never asked to *find* a pattern, only (elsewhere) to name one mining found.
func gistsFromRoutines(db *sql.DB, date time.Time) ([]string, error) {
	end := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location()).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -GistWindowDays)
	events, err := capture.Range(db, start.Unix(), end.Unix())
	if err != nil {
		return nil, err
	}

	var out []string
	for _, p := range routine.FindPeriodic(events) {
		if len(out) >= maxGists {
			break
		}
		// Only the most consistent handful graduate to a remembered habit.
		if p.Consistency < 0.5 {
			continue
		}
		out = append(out, fmt.Sprintf("Usually opens %s around %s on %s.", p.App, p.Window(), p.Cadence()))
	}
	for _, s := range routine.FindSequences(events) {
		if len(out) >= maxGists {
			break
		}
		if s.Share < 0.5 {
			continue
		}
		out = append(out, fmt.Sprintf("After %s, usually switches to %s.", s.From, s.To))
	}
	return out, nil
}

// downscale multiplies every active memory's salience by DownscaleFactor, floored,
// and reports how many rows would actually move. Deliberately *not* logged per
// memory: it touches every row, and a memory_log line each would bury the
// timeline the log exists to keep legible. Only structural events (merge,
// supersede, new gist) leave a trace.
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

// countArtifacts counts the distinct files, commits, and pages seen in the day —
// the artifacts a full association pass would tie to their projects.
func countArtifacts(events []event.Event) int {
	seen := map[string]bool{}
	for _, e := range events {
		switch e.Kind {
		case event.Commit, event.File:
			if e.Path != "" {
				seen["p"+e.Path] = true
			}
		case event.URL:
			if e.URL != "" {
				seen["u"+e.URL] = true
			}
		}
	}
	return len(seen)
}
