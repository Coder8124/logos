package replay

import "database/sql"

// The last-seen marker: a single row remembering when you last caught up, so the
// next replay knows exactly how far back "since you've been away" reaches. Kept
// in its own tiny table rather than a shared settings blob — one fact, one place.
//
// Deliberately cache-only, and the one table that may be: it is a read cursor,
// not something the user knows or decided. Losing it on a rebuild loses no
// content — the next replay reports itself as a first run and reaches back
// defaultLookbackDays, which shows more than it needs to and never less. A vault
// file for it would also be wrong in the other direction: a vault is plain
// markdown and can sit in a folder that syncs between machines, and "when you
// last caught up" on a laptop is not when you last caught up on the desktop.

const stateSchema = `
CREATE TABLE IF NOT EXISTS replay_state (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    last_seen INTEGER NOT NULL
);`

func Init(db *sql.DB) error {
	_, err := db.Exec(stateSchema)
	return err
}

// LastSeen returns when the user last caught up, and whether a marker exists at
// all (false on the very first run).
func LastSeen(db *sql.DB) (int64, bool) {
	var ts int64
	if err := db.QueryRow("SELECT last_seen FROM replay_state WHERE id = 1").Scan(&ts); err != nil {
		return 0, false
	}
	return ts, true
}

// Mark records that the user has now caught up as of ts, so the next replay
// measures from here.
func Mark(db *sql.DB, ts int64) error {
	_, err := db.Exec(
		`INSERT INTO replay_state (id, last_seen) VALUES (1, ?)
		 ON CONFLICT(id) DO UPDATE SET last_seen = excluded.last_seen`, ts)
	return err
}
