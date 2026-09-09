package session

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/vault"
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

// CloseAbandoned resolves one session doctor has flagged abandoned: it writes
// a minimal checkpoint for it, marked AutoClosed so it can never be mistaken
// for a real handoff, and marks that one session closed.
//
// Refuses anything not already past AbandonAfter — the real failure mode this
// exists to guard against is closing a session that is merely slow, not dead.
// Rather than trust the caller to have checked FindAbandonedInProject first,
// this runs the same query itself and refuses if the id is not in it, so the
// guarantee holds regardless of what the caller looked at.
//
// Only the named session closes. A project can have more than one open
// session — a different agent's own in-progress work — and this must not
// fold a stranger's notes into a checkpoint it never asked for, or mark its
// session ended out from under it. See Commit, which closes every open
// session for a project on purpose: that is right for a checkpoint the
// project's own agent is writing about its own working tree, and wrong here,
// where the caller is resolving one specific session on someone else's
// behalf.
func CloseAbandoned(db *sql.DB, vaultDir, id string) (Checkpoint, error) {
	s, ok, err := Get(db, id)
	if err != nil {
		return Checkpoint{}, err
	}
	if !ok {
		return Checkpoint{}, fmt.Errorf("no session %q", id)
	}
	if s.Ended != 0 {
		return Checkpoint{}, fmt.Errorf("session %q is already closed", id)
	}

	candidates, err := FindAbandonedInProject(db, s.Project, AbandonAfter)
	if err != nil {
		return Checkpoint{}, err
	}
	var a *Abandoned
	for i := range candidates {
		if candidates[i].Session == id {
			a = &candidates[i]
			break
		}
	}
	if a == nil {
		return Checkpoint{}, fmt.Errorf(
			"session %q has not gone silent for %s yet — not safe to auto-close; "+
				"use checkpoint or handoff if this work is actually done", id, AbandonAfter)
	}

	notes, err := Notes(db, id)
	if err != nil {
		return Checkpoint{}, err
	}
	silentHours := int(time.Since(time.Unix(a.LastActivity, 0)).Hours())
	c := Checkpoint{
		Project:    s.Project,
		Agent:      s.Agent,
		Task:       s.Task,
		AutoClosed: true,
		State: fmt.Sprintf(
			"Closed automatically — no activity for %dh, past the %s abandonment window. "+
				"Nobody reviewed or summarized this session; this is not a real handoff.",
			silentHours, AbandonAfter),
		Next: "Read the recorded notes below, if any, and decide whether to continue this work or discard it.",
		TS:   time.Now().Unix(),
	}
	if len(notes) > 0 {
		lines := make([]string, 0, len(notes))
		for _, n := range notes {
			lines = append(lines, "- "+n.Text)
		}
		c.State += "\n\nRecorded during the session:\n" + strings.Join(lines, "\n")
	}

	prev, _ := Latest(vaultDir, s.Project)
	var follows string
	if prev != nil {
		follows = prev.Session
	}

	// The checkpoint's filename is seeded from the abandoned session's own id,
	// not from now — it belongs where the work actually happened, so `brain
	// sessions` and `resume` keep reading history in the order it occurred
	// rather than filing a stale session as the most recent thing that
	// happened just because someone got around to closing it today.
	cpID, path, err := claimCheckpoint(vaultDir, s.Project, s.Agent, s.ID)
	if err != nil {
		return Checkpoint{}, err
	}
	c.Session = cpID
	c.Slug = filepath.ToSlash(filepath.Join(CheckpointDir, s.Project, cpID))
	if err := vault.WriteAtomic(path, []byte(c.Markdown(follows))); err != nil {
		return Checkpoint{}, err
	}
	if err := closeOne(db, id, c.Slug); err != nil {
		return Checkpoint{}, fmt.Errorf(
			"checkpoint written to the vault but the session table was not updated: %w", err)
	}
	// Rewrites the file from whatever is still open — since only the target
	// session was just marked closed, another agent's own uncommitted notes in
	// this same project are untouched by this rewrite. flushNotesIn, not
	// flushNotes: this takes vaultDir directly rather than trusting that
	// something else already called SetVault for this database.
	if err := flushNotesIn(db, vaultDir, s.Project); err != nil {
		return c, fmt.Errorf(
			"checkpoint written but the working-notes file was not updated: %w", err)
	}
	return c, nil
}

func closeOne(db *sql.DB, id, slug string) error {
	_, err := db.Exec(`UPDATE sessions SET ended = ?, slug = ? WHERE id = ?`, time.Now().Unix(), slug, id)
	return err
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
