// Package session records what an agent was doing, so a different agent can
// pick it up.
//
// Memory answers "what do you know about X". That is only half of continuity —
// the other half is "where did we stop, and what had we already ruled out". An
// agent that has to rediscover the three approaches that failed yesterday is not
// resuming, it is starting over with a warmer greeting.
//
// The split mirrors git, which is the shape logos is aiming at:
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
	"unicode"
)

// A Session is one agent's working stretch on one project. It exists to give
// working notes something to hang off and to record who was at the keyboard —
// a handoff is meaningless without knowing who is handing off.
type Session struct {
	ID string // "20260814-143207-claude", lexically sortable by time
	// Project is the scope the work is filed under: a project name, or
	// "project/worktree" when it is happening in a linked git worktree. Two
	// parallel worktrees are the same codebase and the same memory, but not the
	// same working stretch, and resuming into the other one's checkpoint is a
	// wrong handoff rather than an untidy one.
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
	// Agent is denormalised from the session because notes outlive the agent
	// that wrote them — when work passes between two agents, which of them
	// found a thing is part of the finding.
	Agent string
	Text  string
	TS    int64
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
	// A name made entirely of punctuation or emoji survives sanitisation as the
	// empty string. Saying "a session needs a project" there blames the caller
	// for omitting what they supplied, which is how this took a while to
	// diagnose the first time.
	slug := safeScope(project)
	if slug == "" {
		return Session{}, fmt.Errorf(
			"project name %q has no letters or digits to make a filename from", project)
	}
	now := time.Now()
	s := Session{
		Project: slug,
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

// Current returns this agent's open session for a project, opening one if there
// is none. Agents call note_progress without ceremony; making them manage a
// session lifecycle would mean they simply wouldn't.
//
// Scoped to the agent, not just the project. A session is one agent's working
// stretch: when a previous agent died without committing, its session is still
// open, and handing it to whoever arrives next would file their checkpoint
// under the dead agent's id — so the record of a handoff would name the wrong
// author. Their uncommitted notes are still picked up; see Commit.
func Current(db *sql.DB, project, agent string) (Session, error) {
	project = safeScope(project)
	if strings.TrimSpace(agent) == "" {
		agent = "agent"
	}
	var s Session
	err := db.QueryRow(
		`SELECT id, project, agent, task, started, ended, slug
		 FROM sessions WHERE project = ? AND agent = ? AND ended = 0
		 ORDER BY started DESC LIMIT 1`, project, agent).
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

// OpenCount reports how many sessions, across every project, are still open —
// started but not yet folded into a checkpoint by Commit. It is what lets a
// caller say "capture is live" from real state rather than from the mere
// presence of a process, the way a terminal-style inspector needs to.
func OpenCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE ended = 0`).Scan(&n)
	return n, err
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
	return AddNoteAt(db, project, agent, text, time.Now().Unix())
}

// AddNoteAt appends a note that happened at a given time rather than now.
//
// Backdating exists for the cases where the writing and the doing are not the
// same moment: importing a trail from elsewhere, replaying a transcript, or a
// benchmark that needs "thirteen days ago" to actually be thirteen days ago.
// Anything that reasons about how old a note is depends on this being real.
func AddNoteAt(db *sql.DB, project, agent, text string, ts int64) (Note, error) {
	n, err := addNoteNoFlush(db, project, agent, text, ts)
	if err != nil {
		return Note{}, err
	}
	// And then to the vault, because a note that lives only in the index is a
	// note that `logos index` is entitled to throw away. The note is returned
	// alongside the error: it is in the cache and usable, it is just not yet
	// durable, and the caller deserves to be told which of those is true.
	if err := flushNotes(db, project); err != nil {
		return n, fmt.Errorf("note saved to the index but not to the vault: %w", err)
	}
	return n, nil
}

func addNoteNoFlush(db *sql.DB, project, agent, text string, ts int64) (Note, error) {
	if strings.TrimSpace(text) == "" {
		return Note{}, fmt.Errorf("an empty note records nothing")
	}
	s, err := Current(db, project, agent)
	if err != nil {
		return Note{}, err
	}
	if ts == 0 {
		ts = time.Now().Unix()
	}
	n := Note{Session: s.ID, Agent: s.Agent, Text: strings.TrimSpace(text), TS: ts}
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
	return scanNotes(db.Query(
		`SELECT n.id, n.session, s.agent, n.text, n.ts
		 FROM session_notes n JOIN sessions s ON s.id = n.session
		 WHERE n.session = ? ORDER BY n.ts, n.id`, sessionID))
}

// Uncommitted returns the working notes for a project that no checkpoint has
// captured yet — everything written into still-open sessions, whoever wrote
// them. This is the "uncommitted changes" half of resume: work that happened
// but was never written down properly, including the work of an agent that
// died before it could.
func Uncommitted(db *sql.DB, project string) ([]Note, error) {
	return scanNotes(db.Query(
		`SELECT n.id, n.session, s.agent, n.text, n.ts
		 FROM session_notes n JOIN sessions s ON s.id = n.session
		 WHERE s.project = ? AND s.ended = 0
		 ORDER BY n.ts, n.id`, safeScope(project)))
}

func scanNotes(rows *sql.Rows, err error) ([]Note, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Session, &n.Agent, &n.Text, &n.TS); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ParallelGrace is how long another agent's open session may sit silent before
// a checkpoint treats it as dead and takes its notes.
//
// A checkpoint used to fold in and close *every* open session on the project.
// That is right for the agent that died mid-task — its findings are exactly
// what the next agent is building on, and they reach the durable record only if
// somebody else's checkpoint carries them. It is wrong for an agent that is
// still working: with four agents on one project, whichever checkpointed first
// took all four sets of notes, and the other three checkpoints had no State
// section at all. The handoff of everyone who finished second was filed under
// someone else's task.
//
// The two cases look identical only if the index is assumed to have no
// liveness signal. It has one already: session_notes carries a timestamp, so
// "has said nothing for half an hour" is recorded without adding a heartbeat.
// An agent mid-task writes notes; an agent that is gone does not.
//
// Shorter than AbandonAfter because they answer different questions. That one
// asks when a human should be *told* a session was dropped, and is deliberately
// long enough to sit through a lunch break. This one asks whether a parallel
// agent is still coming back for its own notes, and six hours of silence is far
// past the point where the answer is yes.
const ParallelGrace = 30 * time.Minute

// foldableSessions returns the open sessions on a project whose notes a
// checkpoint by ownSession should take: its own, plus any that has gone silent
// past grace. A session someone is actively working is left out, so its notes
// stay where its own agent's checkpoint will find them.
//
// Silence is measured from the last note, falling back to the session's start
// time, so a session that opened and never recorded anything still ages out
// rather than pinning itself open forever.
func foldableSessions(db *sql.DB, project, ownSession string, grace time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-grace).Unix()
	rows, err := db.Query(`
SELECT s.id
FROM sessions s LEFT JOIN session_notes n ON n.session = s.id
WHERE s.ended = 0 AND s.project = ?
GROUP BY s.id
HAVING s.id = ? OR COALESCE(MAX(n.ts), s.started) < ?
ORDER BY s.id`, safeScope(project), ownSession, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// notesIn returns the working notes held by the given sessions, oldest first,
// as one stream — the order they were written in matters more than which
// session each came from, because the reader is reconstructing what happened.
func notesIn(db *sql.DB, ids []string) ([]Note, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT n.id, n.session, s.agent, n.text, n.ts
	      FROM session_notes n JOIN sessions s ON s.id = n.session
	      WHERE n.session IN (?` + strings.Repeat(",?", len(ids)-1) + `)
	      ORDER BY n.ts, n.id`
	return scanNotes(db.Query(q, args...))
}

// closeSessions marks the given sessions committed and points them at the
// checkpoint that captured them. Only the ones whose notes that checkpoint
// actually took: a session left open by foldableSessions still holds its notes,
// and closing it here would strand them exactly as if they had been folded in.
func closeSessions(db *sql.DB, ids []string, slug string) error {
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := db.Exec(
			`UPDATE sessions SET ended = ?, slug = ? WHERE id = ?`, now, slug, id); err != nil {
			return err
		}
	}
	return nil
}

// safe reduces a name to something usable as a path segment and a session id.
// Project names arrive from agents and from note slugs; neither is trustworthy
// as a filename.
//
// Letters and digits in any script are kept. The rule used to be ASCII-only,
// which quietly made logos unusable for anyone naming a project in Japanese,
// Cyrillic, Arabic, Greek or Hindi: the name reduced to the empty string and
// the caller was then told a project was required, having supplied one.
// Filesystems on every platform logos targets take UTF-8 filenames, so the
// restriction bought nothing.
//
// What is still stripped is what makes a path dangerous or ambiguous:
// separators, dots, and control characters. `..` and `/` cannot survive, which
// is what keeps a checkpoint inside the vault.
func safe(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
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

// safeScope is safe for a scope rather than a plain name: "kestrel" is one, and
// so is "kestrel/feature-x", the same project worked on in a linked worktree.
//
// A scope is what sessions are keyed by and what a checkpoint's path is built
// from, and its two levels have to stay two levels — safe collapses "/" to a
// dash, which would file a worktree as a sibling project called
// "kestrel-feature-x" rather than as a folder inside kestrel's. Each segment is
// still run through safe, so `..` and separators cannot survive and a scope
// cannot climb out of the vault however it was spelled.
// SafeScope is safeScope for callers outside this package that have to key
// something by the same project a checkpoint will be filed under. The MCP
// server counts unsaved progress per project and clears it when that project is
// checkpointed; keyed on the agent's raw spelling, the two halves used
// different keys and a checkpoint never cleared its own notes.
func SafeScope(s string) string { return safeScope(s) }

func safeScope(s string) string {
	parts := strings.Split(s, "/")
	kept := parts[:0]
	for _, p := range parts {
		if p = safe(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "/")
}
