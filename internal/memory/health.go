package memory

import (
	"database/sql"
	"fmt"
	"time"
)

// Memory health — git status for what the assistant knows.
//
// Cheap, deterministic checks over the store that say where it needs tending:
// near-duplicates that slipped past write-time dedup, facts gone stale, hunches
// corroboration never firmed up, and memories connected to nothing in the vault.
// No model runs; every number is a query. The score is the fraction of memories
// carrying no defect flag — orphans are reported but not counted against it, since
// a memory that happens to name no note isn't wrong, just unlinked.

const (
	// StaleAgeDays: a memory older than this, never recalled, and faded in
	// effective salience is stale — still true, perhaps, but no longer earning
	// its keep.
	StaleAgeDays  = 60
	staleSalience = 0.3
	lowConfidence = 0.6
)

// HealthReport is the computed diagnostic.
type HealthReport struct {
	Total          int        `json:"total"`
	Duplicates     int        `json:"duplicates"`      // memories that are part of a near-duplicate pair
	DuplicatePairs [][2]int64 `json:"duplicate_pairs"` // a sample, for `consolidate` to act on
	Stale          int        `json:"stale"`
	StaleIDs       []int64    `json:"stale_ids"` // a sample
	Orphans        int        `json:"orphans"`   // linked to no note (informational)
	LowConfidence  int        `json:"low_confidence"`
	Score          float64    `json:"score"` // 0..1, fraction with no defect flag
}

// Health computes the report over the active store. It loads the columns the
// checks need directly (activeMemories omits confidence), including the vectors
// the duplicate check compares.
func Health(db *sql.DB) (HealthReport, error) {
	// quarantined = 0: a pending memory's low confidence is expected, not a
	// defect — flagging it here would tell the user to fix something that
	// `brain review` already covers.
	rows, err := db.Query(
		`SELECT id, salience, confidence, created, last_used, uses, vec FROM memories WHERE superseded = 0 AND quarantined = 0`)
	if err != nil {
		return HealthReport{}, err
	}
	var mems []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Salience, &m.Confidence, &m.Created, &m.LastUsed, &m.Uses, &m.vec); err != nil {
			rows.Close()
			return HealthReport{}, err
		}
		mems = append(mems, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return HealthReport{}, err
	}

	now := time.Now().Unix()
	rep := HealthReport{Total: len(mems)}
	flagged := map[int64]bool{} // memories with a real defect (drives the score)

	for _, m := range mems {
		// Low confidence: a hunch corroboration never firmed up.
		if m.Confidence < lowConfidence {
			rep.LowConfidence++
			flagged[m.ID] = true
		}
		// Stale: never recalled, old, and faded.
		ageDays := float64(now-m.Created) / 86400
		if m.Uses == 0 && ageDays > StaleAgeDays && EffectiveSalience(m, now) < staleSalience {
			rep.Stale++
			if len(rep.StaleIDs) < 10 {
				rep.StaleIDs = append(rep.StaleIDs, m.ID)
			}
			flagged[m.ID] = true
		}
	}

	// Duplicates: near-identical pairs that both remain active — the same cosine
	// gate write-time dedup uses. O(n²) over the active set, which stays small.
	dup := map[int64]bool{}
	for i := 0; i < len(mems); i++ {
		for j := i + 1; j < len(mems); j++ {
			if len(mems[i].vec) == 0 || len(mems[j].vec) == 0 {
				continue
			}
			if cosine(blobToFloats(mems[i].vec), blobToFloats(mems[j].vec)) >= DedupThreshold {
				if len(rep.DuplicatePairs) < 10 {
					rep.DuplicatePairs = append(rep.DuplicatePairs, [2]int64{mems[i].ID, mems[j].ID})
				}
				dup[mems[i].ID] = true
				dup[mems[j].ID] = true
			}
		}
	}
	for id := range dup {
		flagged[id] = true
	}
	rep.Duplicates = len(dup)

	// Orphans: active memories linked to no note in the graph. Best-effort — if
	// the note/graph tables aren't built, skip it rather than fail the report.
	// Reported for awareness, not counted against the score.
	if g, err := BuildGraph(db, false); err == nil {
		deg := make(map[string]int, len(g.Nodes))
		for _, n := range g.Nodes {
			deg[n.ID] = n.Degree
		}
		for _, m := range mems {
			// BuildGraph keys memory nodes as "m<id>" (see graph.go).
			if deg[fmt.Sprintf("m%d", m.ID)] == 0 {
				rep.Orphans++
			}
		}
	}

	rep.Score = 1
	if rep.Total > 0 {
		rep.Score = 1 - float64(len(flagged))/float64(rep.Total)
	}
	return rep, nil
}
