package memory

import (
	"database/sql"
	"strings"
)

// The memory diff — "what changed?" over a window.
//
// It reads the append-only memory_log, which snapshots each memory's text at
// every lifecycle event, so the history stays legible even after a memory is
// deleted. The changes sort into three buckets a person actually asks about:
// what was newly learned, what was dropped, and what got corroborated. Pure
// arithmetic over the log — no model runs, so a diff is instant and offline, the
// same discipline as the brief and the weekly review. Narration over the result
// is a separate, optional step; the computed list is the product.

// DiffEntry is one change to one memory within the window.
type DiffEntry struct {
	MemID int64  `json:"mem_id"`
	Event string `json:"event"`
	Text  string `json:"text"` // snapshot from the log at the time of the change
	TS    int64  `json:"ts"`
}

// DiffResult is what changed between two moments.
type DiffResult struct {
	Subject      string      `json:"subject,omitempty"`
	Since        int64       `json:"since"`
	Until        int64       `json:"until"`
	Added        []DiffEntry `json:"added"`        // newly learned facts
	Removed      []DiffEntry `json:"removed"`      // superseded or forgotten
	Corroborated []DiffEntry `json:"corroborated"` // re-stated, merged, or updated — belief firmed
}

func (d DiffResult) Empty() bool {
	return len(d.Added)+len(d.Removed)+len(d.Corroborated) == 0
}

// LearnedBetween counts memories first created in [since, until] (unix seconds),
// read from the append-only log so the count is stable even after some of those
// memories were later superseded or forgotten. Used for growth over time.
func LearnedBetween(db *sql.DB, since, until int64) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM memory_log WHERE event = ? AND ts >= ? AND ts <= ?`,
		EvCreated, since, until).Scan(&n)
	return n, err
}

// Diff computes what changed in the store between since and until (unix seconds,
// inclusive). If subject is non-empty, it narrows to changes whose snapshot text
// mentions it, case-insensitively — "what changed about Sarah?". Matching on the
// snapshot (not the live memories table) is deliberate: it still catches facts
// that were forgotten or superseded inside the window.
func Diff(db *sql.DB, subject string, since, until int64) (DiffResult, error) {
	res := DiffResult{Subject: subject, Since: since, Until: until}

	rows, err := db.Query(
		`SELECT mem_id, event, COALESCE(detail,''), ts FROM memory_log
		 WHERE ts >= ? AND ts <= ? ORDER BY ts ASC, id ASC`, since, until)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	needle := strings.ToLower(strings.TrimSpace(subject))
	for rows.Next() {
		var e DiffEntry
		if err := rows.Scan(&e.MemID, &e.Event, &e.Text, &e.TS); err != nil {
			return res, err
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Text), needle) {
			continue
		}
		switch e.Event {
		case EvCreated:
			res.Added = append(res.Added, e)
		case EvSuperseded, EvForgotten:
			res.Removed = append(res.Removed, e)
		case EvReinforced, EvMerged, EvUpdated:
			// The survivor of a merge and a re-stated fact both got *stronger*, not
			// removed — corroboration, the same bucket as an explicit update.
			res.Corroborated = append(res.Corroborated, e)
		}
	}
	return res, rows.Err()
}
