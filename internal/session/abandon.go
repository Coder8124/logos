package session

import (
	"database/sql"
	"time"
)

// Abandonment: the case Uncommitted only half-covers.
//
// Uncommitted answers "what has been recorded but not committed" — the notes
// themselves. It says nothing about the session that wrote them: whether it is
// still being worked, or whether the agent that opened it is simply gone. A
// session with three findings in it and no activity for a day did not pause, it
// died, and the notes it holds are invisible to anyone who does not think to go
// looking for them. This is what makes that visible.

// AbandonAfter is how long an open session may sit with no new activity before
// it counts as abandoned rather than merely paused. Long enough that a lunch
// break, an overnight stop, or a slow afternoon is not flagged; short enough
// that a session that died mid-task is caught the same day rather than
// discovered a month later when nobody remembers what it was doing.
const AbandonAfter = 6 * time.Hour

// An Abandoned session is one that opened, may or may not have accumulated
// notes, and then went silent past AbandonAfter without ever being committed.
type Abandoned struct {
	Session string
	Project string
	Agent   string
	Task    string
	// Notes is how much work is sitting in it — the reason this matters. Zero
	// is still worth reporting: a session opened and never used is a smaller
	// story than one holding findings, but it is still an agent that did not
	// hand off cleanly.
	Notes int
	// LastActivity is the last note's timestamp, or the session's start time if
	// it never got one. Measuring from here rather than from Started is what
	// keeps a long but continuously-active session from being flagged: what
	// matters is silence, not duration.
	LastActivity int64
}

// FindAbandoned returns every open session across the whole vault that has
// gone silent for longer than after. This is the query the health check and
// the continuity report both run — the vault-wide view of "what got dropped".
func FindAbandoned(db *sql.DB, after time.Duration) ([]Abandoned, error) {
	cutoff := time.Now().Add(-after).Unix()
	rows, err := db.Query(`
SELECT s.id, s.project, s.agent, s.task, COUNT(n.id), COALESCE(MAX(n.ts), s.started)
FROM sessions s LEFT JOIN session_notes n ON n.session = s.id
WHERE s.ended = 0
GROUP BY s.id
HAVING COALESCE(MAX(n.ts), s.started) < ?
ORDER BY COALESCE(MAX(n.ts), s.started) ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	return scanAbandoned(rows)
}

// FindAbandonedInProject narrows FindAbandoned to one project — the view
// `brain sessions <project>` surfaces, where an agent (or a person) is looking
// at one piece of work rather than the whole vault.
func FindAbandonedInProject(db *sql.DB, project string, after time.Duration) ([]Abandoned, error) {
	cutoff := time.Now().Add(-after).Unix()
	rows, err := db.Query(`
SELECT s.id, s.project, s.agent, s.task, COUNT(n.id), COALESCE(MAX(n.ts), s.started)
FROM sessions s LEFT JOIN session_notes n ON n.session = s.id
WHERE s.ended = 0 AND s.project = ?
GROUP BY s.id
HAVING COALESCE(MAX(n.ts), s.started) < ?
ORDER BY COALESCE(MAX(n.ts), s.started) ASC`, safeScope(project), cutoff)
	if err != nil {
		return nil, err
	}
	return scanAbandoned(rows)
}

func scanAbandoned(rows *sql.Rows) ([]Abandoned, error) {
	defer rows.Close()
	var out []Abandoned
	for rows.Next() {
		var a Abandoned
		if err := rows.Scan(&a.Session, &a.Project, &a.Agent, &a.Task, &a.Notes, &a.LastActivity); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
