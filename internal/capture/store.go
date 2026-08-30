package capture

import (
	"database/sql"
	"strings"
)

// Schema lives in the same SQLite file as the vault index but is conceptually
// separate: the index is a cache derived from markdown, this is primary data
// that exists nowhere else. That is why retention is a deliberate setting
// rather than a cleanup detail.
const Schema = `
CREATE TABLE IF NOT EXISTS events (
    id     INTEGER PRIMARY KEY,
    ts     INTEGER NOT NULL,
    kind   TEXT    NOT NULL,
    app    TEXT,
    title  TEXT,
    url    TEXT,
    path   TEXT,
    dur_s  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS events_ts   ON events(ts);
CREATE INDEX IF NOT EXISTS events_kind ON events(kind, ts);

-- High-water marks so pulling browser history stays incremental instead of
-- rescanning the whole file every poll.
CREATE TABLE IF NOT EXISTS source_state (
    source TEXT PRIMARY KEY,
    cursor INTEGER NOT NULL
);
`

func InitStore(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

func Insert(db *sql.DB, e Event) error {
	_, err := db.Exec(
		`INSERT INTO events (ts, kind, app, title, url, path, dur_s) VALUES (?,?,?,?,?,?,?)`,
		e.TS, string(e.Kind), nullable(e.App), nullable(e.Title), nullable(e.URL), nullable(e.Path), e.DurS)
	return err
}

func InsertMany(db *sql.DB, events []Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO events (ts, kind, app, title, url, path, dur_s) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.Exec(e.TS, string(e.Kind), nullable(e.App), nullable(e.Title),
			nullable(e.URL), nullable(e.Path), e.DurS); err != nil {
			return 0, err
		}
	}
	return len(events), tx.Commit()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func Cursor(db *sql.DB, source string) int64 {
	var c int64
	if err := db.QueryRow("SELECT cursor FROM source_state WHERE source = ?", source).Scan(&c); err != nil {
		return 0
	}
	return c
}

func SetCursor(db *sql.DB, source string, cursor int64) error {
	_, err := db.Exec(
		`INSERT INTO source_state (source, cursor) VALUES (?,?)
		 ON CONFLICT(source) DO UPDATE SET cursor = excluded.cursor`, source, cursor)
	return err
}

// Range returns events in a window, oldest first.
func Range(db *sql.DB, from, to int64) ([]Event, error) {
	rows, err := db.Query(
		`SELECT id, ts, kind, app, title, url, path, dur_s FROM events
		 WHERE ts >= ? AND ts < ? ORDER BY ts`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var kind string
		var app, title, url, path sql.NullString
		if err := rows.Scan(&e.ID, &e.TS, &kind, &app, &title, &url, &path, &e.DurS); err != nil {
			return nil, err
		}
		e.Kind = Kind(kind)
		e.App, e.Title, e.URL, e.Path = app.String, title.String, url.String, path.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune drops raw events past the retention window. Rollups must already have
// extracted anything worth keeping — that is the whole contract of the two-tier
// design.
func Prune(db *sql.DB, olderThanTS int64) (int, error) {
	res, err := db.Exec("DELETE FROM events WHERE ts < ?", olderThanTS)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func Count(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&n)
	return n, err
}

// Footprint is what raw capture currently costs, measured rather than guessed.
//
// A user deciding whether to leave the daemon running deserves a number, and
// the only honest number is one taken from their own data: how much text their
// own apps and URLs actually produce. Bytes is the stored text plus a fixed
// allowance for the integer columns and row overhead — close enough to size a
// disk budget, and never presented as exact.
//
// Days is how long the existing events span. It is the reason this returns a
// span at all: with less than a day of data there is nothing to extrapolate
// from, and saying so is better than projecting a week from ten minutes.
func Footprint(db *sql.DB) (events int, bytes int64, days float64, err error) {
	// 48 bytes covers id, ts, dur_s, the row header and the two indexes' share.
	const rowOverhead = 48
	var size sql.NullInt64
	var lo, hi sql.NullInt64
	err = db.QueryRow(`
		SELECT COUNT(*),
		       SUM(LENGTH(COALESCE(app,'')) + LENGTH(COALESCE(title,''))
		           + LENGTH(COALESCE(url,'')) + LENGTH(COALESCE(path,'')) + ?),
		       MIN(ts), MAX(ts)
		FROM events`, rowOverhead).Scan(&events, &size, &lo, &hi)
	if err != nil {
		return 0, 0, 0, err
	}
	if size.Valid {
		bytes = size.Int64
	}
	if lo.Valid && hi.Valid && hi.Int64 > lo.Int64 {
		days = float64(hi.Int64-lo.Int64) / 86400
	}
	return events, bytes, days, nil
}

// ByIDs fetches specific events, for showing the evidence behind a proposal.
// Ordered by time rather than by id so the reader sees a narrative.
func ByIDs(db *sql.DB, ids []int64) ([]Event, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := db.Query(
		`SELECT id, ts, kind, app, title, url, path, dur_s FROM events
		 WHERE id IN (`+strings.Join(placeholders, ",")+`) ORDER BY ts`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var kind string
		var app, title, url, path sql.NullString
		if err := rows.Scan(&e.ID, &e.TS, &kind, &app, &title, &url, &path, &e.DurS); err != nil {
			return nil, err
		}
		e.Kind = Kind(kind)
		e.App, e.Title, e.URL, e.Path = app.String, title.String, url.String, path.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReplaceCalendarWindow swaps out the captured calendar window wholesale.
//
// Calendar is the one source that captures the future, and the future changes:
// meetings move, get cancelled, get added. Cursoring an append-only table would
// accumulate stale ghosts of rescheduled meetings. Deleting everything from
// `from` onward and re-inserting the fresh window keeps the stored view honest
// and idempotent — re-polling never duplicates a meeting.
func ReplaceCalendarWindow(db *sql.DB, from int64, events []Event) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM events WHERE kind = 'calendar' AND ts >= ?", from); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range events {
		if e.TS < from {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO events (ts, kind, app, title, url, path, dur_s) VALUES (?,?,?,?,?,?,?)`,
			e.TS, string(e.Kind), nullable(e.App), nullable(e.Title), nil, nil, 0); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}
