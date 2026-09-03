package memory

import (
	"database/sql"
	"time"
)

// The memory timeline — git history for what the assistant knows. Every change
// to the store leaves a line here: when a fact was first learned, each time it
// was corroborated, when a newer fact superseded it, when duplicates merged, and
// when something was forgotten. Unlike the memories table (which holds current
// truth), this log is append-only and never rewritten, so you can always answer
// "when did it start believing this, and why does it believe it now?".

// Event names for the log. Kept as plain strings so the log stays readable and
// new event types never require a migration.
const (
	EvCreated     = "created"     // a new memory was stored
	EvReinforced  = "reinforced"  // a re-statement corroborated an existing memory
	EvSuperseded  = "superseded"  // a newer memory replaced this one (ref_id is the newer)
	EvMerged      = "merged"      // a duplicate was folded into this one (ref_id is the dropped)
	EvForgotten   = "forgotten"   // the memory was deleted
	EvUpdated     = "updated"     // an attribute (confidence, project, salience) changed
	EvQuarantined = "quarantined" // a machine proposed this and it is waiting for review
	EvAccepted    = "accepted"    // a quarantined memory was reviewed and released into active memory
	EvRejected    = "rejected"    // a quarantined memory was reviewed and discarded
)

// LogEntry is one line of the timeline.
type LogEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	MemID  int64  `json:"mem_id"`
	Event  string `json:"event"`
	Detail string `json:"detail"`
	RefID  int64  `json:"ref_id"`
}

// logEvent appends one line. It is a bare INSERT with no open cursor, safe to
// call from anywhere including right after a mutation — never from inside an
// open rows loop (the single-connection pool would deadlock).
func logEvent(db *sql.DB, memID int64, event, detail string, refID int64) {
	db.Exec(`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id) VALUES (?,?,?,?,?)`,
		time.Now().Unix(), memID, event, detail, refID)
}

// Timeline returns the most recent log entries, newest first, up to limit
// (limit <= 0 means all).
func Timeline(db *sql.DB, limit int) ([]LogEntry, error) {
	q := `SELECT id, ts, mem_id, event, detail, ref_id FROM memory_log ORDER BY ts DESC, id DESC`
	if limit > 0 {
		q += " LIMIT " + itoa(limit)
	}
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.MemID, &e.Event, &e.Detail, &e.RefID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// History returns the timeline for a single memory, oldest first — its whole
// life, from first learned to now.
func History(db *sql.DB, memID int64) ([]LogEntry, error) {
	rows, err := db.Query(
		`SELECT id, ts, mem_id, event, detail, ref_id FROM memory_log WHERE mem_id = ? OR ref_id = ? ORDER BY ts ASC, id ASC`,
		memID, memID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.MemID, &e.Event, &e.Detail, &e.RefID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
