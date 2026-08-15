// Package session records what an agent was doing, so a different agent can
// pick it up.
//
// Memory answers "what do you know about X". That is only half of continuity —
// the other half is "where did we stop, and what had we already ruled out". An
// agent that has to rediscover the three approaches that failed yesterday is not
// resuming, it is starting over with a warmer greeting.
//
// The split mirrors git, which is the shape brain is aiming at:
//
//	working notes  →  SQLite   →  the working tree. Cheap, frequent, disposable.
//	checkpoints    →  the vault →  commits. Durable, readable, diffable.
//
// So a running agent scribbles freely with AddNote and pays nothing, and Commit
// is the deliberate act that turns that scribbling into a markdown note the
// vault owns. Working notes are lost if the index is rebuilt; that is not a bug,
// it is the definition of uncommitted. Checkpoints survive, because the file is
// the record — see checkpoint.go, which reads them back off disk rather than
// trusting a table.
package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// A Session is one agent's working stretch on one project. It exists to give
// working notes something to hang off and to record who was at the keyboard —
// a handoff is meaningless without knowing who is handing off.
type Session struct {
	ID      string // "20260814-143207-claude", lexically sortable by time
	Project string
	Agent   string // free text: "claude", "cursor", "codex", "pragun"
	Task    string
	Started int64
	Ended   int64  // 0 while open
	Slug    string // vault slug of the checkpoint, once committed
}

// A Note is one line of progress inside a session. Deliberately unstructured:
// the moment this needs fields, agents will stop writing them.
type Note struct {
	ID      int64
	Session string
	Text    string
	TS      int64
}

const Schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id      TEXT PRIMARY KEY,
    project TEXT NOT NULL,
    agent   TEXT NOT NULL,
    task    TEXT NOT NULL DEFAULT '',
    started INTEGER NOT NULL,
    ended   INTEGER NOT NULL DEFAULT 0,
    slug    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_project ON sessions(project, started);

CREATE TABLE IF NOT EXISTS session_notes (
    id      INTEGER PRIMARY KEY,
    session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    text    TEXT NOT NULL,
    ts      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS session_notes_session ON session_notes(session, ts);
`

func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

// idFor builds a session id that sorts by time and names its author. Seconds
// are included because two agents can easily check in within the same minute.
func idFor(agent string, t time.Time) string {
	return t.Format("20060102-150405") + "-" + safe(agent)
}

// Start opens a session. Agent defaults to "agent" rather than erroring: a
// caller that cannot be bothered to identify itself should still be able to
// leave a trail, and an anonymous trail beats no trail.
func Start(db *sql.DB, project, agent, task string) (Session, error) {
	if strings.TrimSpace(project) == "" {
		return Session{}, fmt.Errorf("a session needs a project")
	}
	if strings.TrimSpace(agent) == "" {
		agent = "agent"
	}
	now := time.Now()
	s := Session{
		Project: safe(project),
		Agent:   agent,
		Task:    task,
		Started: now.Unix(),
	}
	// The id is also the checkpoint's filename, so a collision would silently
	// overwrite someone's record of their work. Two commits inside one second by
	// one agent is unlikely but entirely possible under a script; walk the clock
	// forward until the insert takes rather than trusting that it won't happen.
	for i := 0; i < 60; i++ {
		s.ID = idFor(agent, now.Add(time.Duration(i)*time.Second))
		_, err := db.Exec(
			`INSERT INTO sessions (id, project, agent, task, started) VALUES (?,?,?,?,?)`,
			s.ID, s.Project, s.Agent, s.Task, s.Started)
		if err == nil {
			return s, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Session{}, err
		}
	}
	return Session{}, fmt.Errorf("could not allocate a session id for %s", project)
}

// Current returns the open session for a project, opening one if there is none.
// Agents call note_progress without ceremony; making them manage a session
// lifecycle would mean they simply wouldn't.
func Current(db *sql.DB, project, agent string) (Session, error) {
	project = safe(project)
	var s Session
	err := db.QueryRow(
		`SELECT id, project, agent, task, started, ended, slug
		 FROM sessions WHERE project = ? AND ended = 0
		 ORDER BY started DESC LIMIT 1`, project).
		Scan(&s.ID, &s.Project, &s.Agent, &s.Task, &s.Started, &s.Ended, &s.Slug)
	if err == nil {
		return s, nil
	}
	if err != sql.ErrNoRows {
		return Session{}, err
	}
	return Start(db, project, agent, "")
}

// Get loads one session by id.
func Get(db *sql.DB, id string) (Session, bool, error) {
	var s Session
	err := db.QueryRow(
		`SELECT id, project, agent, task, started, ended, slug FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.Project, &s.Agent, &s.Task, &s.Started, &s.Ended, &s.Slug)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	return s, err == nil, err
}

// SetTask records what the session is for. Agents usually learn this a few
// turns in, not at the start, so it is a separate call rather than required
// up front.
func SetTask(db *sql.DB, id, task string) error {
	_, err := db.Exec(`UPDATE sessions SET task = ? WHERE id = ?`, task, id)
	return err
}

// AddNote appends one line of progress to the project's open session.
func AddNote(db *sql.DB, project, agent, text string) (Note, error) {
	if strings.TrimSpace(text) == "" {
		return Note{}, fmt.Errorf("an empty note records nothing")
	}
	s, err := Current(db, project, agent)
	if err != nil {
		return Note{}, err
	}
	n := Note{Session: s.ID, Text: strings.TrimSpace(text), TS: time.Now().Unix()}
	res, err := db.Exec(
		`INSERT INTO session_notes (session, text, ts) VALUES (?,?,?)`, n.Session, n.Text, n.TS)
	if err != nil {
		return Note{}, err
	}
	n.ID, _ = res.LastInsertId()
	return n, nil
}

// Notes returns a session's working notes, oldest first — the order they were
// written is the order they make sense in.
func Notes(db *sql.DB, sessionID string) ([]Note, error) {
	rows, err := db.Query(
		`SELECT id, session, text, ts FROM session_notes WHERE session = ? ORDER BY ts, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Session, &n.Text, &n.TS); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Uncommitted returns the working notes for a project that no checkpoint has
// captured yet — everything written into still-open sessions. This is the
// "uncommitted changes" half of resume: work that happened but was never
// written down properly.
func Uncommitted(db *sql.DB, project string) ([]Note, error) {
	rows, err := db.Query(
		`SELECT n.id, n.session, n.text, n.ts
		 FROM session_notes n JOIN sessions s ON s.id = n.session
		 WHERE s.project = ? AND s.ended = 0
		 ORDER BY n.ts, n.id`, safe(project))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Session, &n.Text, &n.TS); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// close marks a session committed and records where its checkpoint landed.
func close_(db *sql.DB, id, slug string) error {
	_, err := db.Exec(
		`UPDATE sessions SET ended = ?, slug = ? WHERE id = ?`, time.Now().Unix(), slug, id)
	return err
}

// safe reduces a name to something usable as a path segment and a session id.
// Project names arrive from agents and from note slugs; neither is trustworthy
// as a filename.
func safe(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
