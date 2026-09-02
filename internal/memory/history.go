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
	EvCreated    = "created"    // a new memory was stored
	EvReinforced = "reinforced" // a re-statement corroborated an existing memory
	EvSuperseded = "superseded" // a newer memory replaced this one (ref_id is the newer)
	EvMerged     = "merged"     // a duplicate was folded into this one (ref_id is the dropped)
	EvForgotten  = "forgotten"  // the memory was deleted
	EvUpdated    = "updated"    // an attribute (confidence, project, salience) changed
)

// LogEntry is one line of the timeline.
type LogEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	MemID  int64  `json:"mem_id"`
	Event  string `json:"event"`
	Detail string `json:"detail"`
	RefID  int64  `json:"ref_id"`
	// Project is the memory's project as it stood when the event happened,
	// which is not always its project now — a memory can be re-scoped, and the
	// timeline of a project should show what was true at the time rather than
	// retroactively rewriting itself.
	Project string `json:"project,omitempty"`
}

// logEvent appends one line. It is a bare INSERT with no open cursor, safe to
// call from anywhere including right after a mutation — never from inside an
// open rows loop (the single-connection pool would deadlock).
func logEvent(db *sql.DB, memID int64, event, detail string, refID int64) {
	var project string
	db.QueryRow("SELECT project FROM memories WHERE id = ?", memID).Scan(&project)
	logEventIn(db, memID, event, detail, refID, project)
}

// logEventIn is logEvent for a caller that already knows the project, which in
// practice means Forget: the row is deleted before the event is written, so by
// the time logEvent would look the project up there is nothing to look up. The
// project has to be carried in from the snapshot the caller took beforehand.
func logEventIn(db *sql.DB, memID int64, event, detail string, refID int64, project string) {
	db.Exec(`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id, project) VALUES (?,?,?,?,?,?)`,
		time.Now().Unix(), memID, event, detail, refID, project)
}

// Timeline returns the most recent log entries, newest first, up to limit
// (limit <= 0 means all).
func Timeline(db *sql.DB, limit int) ([]LogEntry, error) {
	return timeline(db, "", limit)
}

// TimelineInProject is Timeline narrowed to one project: what changed in what
// the assistant knows about *this* work, newest first.
//
// It is a separate function rather than a filter the caller applies afterwards
// because the limit has to be applied after the narrowing, not before. Asking
// for "the last 20 events in kestrel" and getting two, because the other
// eighteen of the global last-20 belonged to other projects, is the kind of
// wrong that looks like an empty project rather than a bad query.
//
// Unattributed lines — written before the log carried a project, by a memory
// since forgotten — match nothing and are simply absent. UnattributedCount
// reports how many there are so a caller can say the timeline is partial
// instead of implying it is complete.
func TimelineInProject(db *sql.DB, project string, limit int) ([]LogEntry, error) {
	return timeline(db, project, limit)
}

// UnattributedCount reports how many log lines carry no project at all. These
// are pre-migration events for memories that were forgotten before the backfill
// could reach them; they belong to some project, and there is no longer any way
// to say which. A project timeline that quietly omits them is accurate about
// what it shows and incomplete about the past, and a caller that says so is
// more trustworthy than one that does not.
func UnattributedCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM memory_log WHERE project = ''").Scan(&n)
	return n, err
}

func timeline(db *sql.DB, project string, limit int) ([]LogEntry, error) {
	q := `SELECT id, ts, mem_id, event, detail, ref_id, project FROM memory_log`
	var args []any
	if project != "" {
		q += " WHERE project = ?"
		args = append(args, project)
	}
	q += " ORDER BY ts DESC, id DESC"
	if limit > 0 {
		q += " LIMIT " + itoa(limit)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.MemID, &e.Event, &e.Detail, &e.RefID, &e.Project); err != nil {
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
		`SELECT id, ts, mem_id, event, detail, ref_id, project FROM memory_log WHERE mem_id = ? OR ref_id = ? ORDER BY ts ASC, id ASC`,
		memID, memID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.MemID, &e.Event, &e.Detail, &e.RefID, &e.Project); err != nil {
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
