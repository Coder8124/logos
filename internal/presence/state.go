package presence

import (
	"database/sql"
	"fmt"
	"time"
)

// The presence's restraint, made concrete. Two pieces of state: when it last
// spoke unprompted (so non-urgent nudges are spaced apart, not quota'd), and what
// it has already said (so the same meeting or loop is never raised twice).
//
// The unit throughout is one unprompted spoken interjection — never your messages,
// never tokens. Anything you initiate is unlimited; only the assistant's own
// volunteered speech is governed here.

const schema = `
CREATE TABLE IF NOT EXISTS presence_state (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    last_spoken INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS presence_spoken (
    key TEXT PRIMARY KEY,
    ts  INTEGER NOT NULL
);`

func Init(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}

// Prefs is the presence's tuning, resolved from the flavor config.
type Prefs struct {
	Interjections bool
	LeadMinutes   int // how far ahead a meeting is flagged
	MinGapMinutes int // minimum quiet gap between non-urgent interjections
	QuietStart    string
	QuietEnd      string // "22:00" .. "08:00"; empty means no quiet hours
}

// Next returns the one thing the presence should say right now, or nil if it
// should stay quiet. It walks the candidates most-urgent first, skips anything
// already said, and applies the restraint rules — deferring to focus, honouring
// quiet hours, and spacing non-urgent nudges — before committing to speak. An
// imminent meeting overrides all of that: a meeting is a meeting.
//
// The returned interjection is recorded as spoken, so calling Next again won't
// repeat it and the cooldown clock is reset.
func Next(db *sql.DB, now time.Time, p Prefs, focused bool) (*Interjection, error) {
	if !p.Interjections {
		return nil, nil
	}
	items, err := Check(db, now, p.LeadMinutes)
	if err != nil {
		return nil, err
	}
	for i := range items {
		it := items[i]
		if alreadySpoken(db, it.Key) {
			continue
		}
		if !eligible(db, now, p, focused, it.Critical) {
			continue
		}
		markSpoken(db, now.Unix(), it.Key)
		return &it, nil
	}
	return nil, nil
}

// eligible decides whether a non-critical nudge may be spoken now. Critical ones
// (imminent meetings) skip every gate.
func eligible(db *sql.DB, now time.Time, p Prefs, focused, critical bool) bool {
	if critical {
		return true
	}
	if focused {
		return false // rule: defer to deep focus
	}
	if inQuietHours(now, p) {
		return false
	}
	gap := time.Duration(p.MinGapMinutes) * time.Minute
	return now.Unix()-lastSpoken(db) >= int64(gap.Seconds())
}

func lastSpoken(db *sql.DB) int64 {
	var ts int64
	db.QueryRow("SELECT last_spoken FROM presence_state WHERE id = 1").Scan(&ts)
	return ts
}

func markSpoken(db *sql.DB, ts int64, key string) {
	db.Exec(`INSERT INTO presence_state (id, last_spoken) VALUES (1, ?)
	         ON CONFLICT(id) DO UPDATE SET last_spoken = excluded.last_spoken`, ts)
	db.Exec(`INSERT OR IGNORE INTO presence_spoken (key, ts) VALUES (?, ?)`, key, ts)
}

func alreadySpoken(db *sql.DB, key string) bool {
	var n int
	db.QueryRow("SELECT COUNT(*) FROM presence_spoken WHERE key = ?", key).Scan(&n)
	return n > 0
}

// inQuietHours reports whether now falls in the configured silent window,
// handling a window that wraps past midnight (22:00 .. 08:00).
func inQuietHours(now time.Time, p Prefs) bool {
	if p.QuietStart == "" || p.QuietEnd == "" {
		return false
	}
	start, ok1 := minutesOfDay(p.QuietStart)
	end, ok2 := minutesOfDay(p.QuietEnd)
	if !ok1 || !ok2 {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if start <= end {
		return cur >= start && cur < end
	}
	// Wraps midnight: quiet if after start OR before end.
	return cur >= start || cur < end
}

func minutesOfDay(hhmm string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
