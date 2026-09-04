package memory

import (
	"database/sql"
	"fmt"
)

// Quarantine is consent before learning applied to the memory a machine writes
// on its own, rather than the memory a person types or a conversation the user
// is actually present for (see internal/consent for that half).
//
// The MCP server used to write straight to the vault: any agent that called
// `remember` mutated the user's real memory with nobody watching. That is the
// same class of problem a code review exists to catch — a change lands, and
// whether it was any good is decided after the fact instead of before. So a
// quarantined memory is a normal row in the memories table (see the
// Quarantined field on Memory) with one property: every read path that feeds
// Recall, All, or AllInProject excludes it, and Store never flushes it to the
// vault file. It exists, but nothing treats it as known until Accept says so.
//
// Deliberately a state on the existing table rather than a second table like
// rollup's proposal queue. A proposal there can be one of four structurally
// different edits to a vault note (new_note/append/new_edge/merge) — none of
// which is shaped like a memory row, so it earns its own table and payload
// type. A quarantined memory has exactly the same shape as an active one; the
// only thing that differs is a flag and what queries are willing to see. Two
// tables would mean two schemas, two dedup paths and two export paths for data
// that is one thing wearing a "pending" sign.

// Pending returns quarantined memories awaiting review, oldest first — a
// backlog should be worked in the order it arrived, not have the newest
// arrival jump the queue.
//
// It carries Agent, which the review queue needs more than active memory does:
// the question in front of a reviewer is whether to believe a proposal, and
// which agent proposed it is part of the answer.
func Pending(db *sql.DB) ([]Memory, error) {
	rows, err := db.Query(
		`SELECT id, text, kind, salience, confidence, project, source, agent, created, last_used, uses
		   FROM memories WHERE quarantined = 1 AND superseded = 0 ORDER BY created ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		var kind string
		if err := rows.Scan(&m.ID, &m.Text, &kind, &m.Salience, &m.Confidence, &m.Project, &m.Source, &m.Agent, &m.Created, &m.LastUsed, &m.Uses); err != nil {
			return nil, err
		}
		m.Kind = Kind(kind)
		m.Quarantined = true
		out = append(out, m)
	}
	return out, rows.Err()
}

// PendingCount is the number `brain doctor` surfaces (see internal/health).
// The whole point of quarantine is that nothing sits there unseen, and a count
// nobody ever looks at is exactly that — so this has to be cheap enough to run
// on every health check, which is why it is a COUNT rather than len(Pending()).
func PendingCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM memories WHERE quarantined = 1 AND superseded = 0").Scan(&n)
	return n, err
}

// Accept releases a quarantined memory into active memory: it becomes
// recallable and gets written to the vault, same as anything Store creates
// directly. Rejects an id that is not actually pending, rather than silently
// no-op-ing on a typo or a double-accept.
func Accept(db *sql.DB, id int64) error {
	var text, kind string
	var quarantined int
	if err := db.QueryRow("SELECT text, kind, quarantined FROM memories WHERE id = ?", id).Scan(&text, &kind, &quarantined); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no memory #%d", id)
		}
		return err
	}
	if quarantined == 0 {
		return fmt.Errorf("memory #%d is not pending review", id)
	}
	// One lock across the whole acceptance, for the reason Store gives: the row
	// stops being quarantined before the file says so, and a reconcile in that
	// window sees an active memory missing from memories/<kind>.md and forgets
	// the thing the user just accepted.
	//
	// The kind lock is taken outside the pending lock, and that order is fixed
	// everywhere the two are nested — it is what keeps two agents accepting at
	// the same moment from deadlocking against each other.
	return withKind(db, Kind(kind), func() error {
		// A quarantined memory is not in the file yet, so accepting it is the
		// moment it starts being written there — and that write is a whole-file
		// rewrite. Adopt any hand edits first.
		if err := reconcileLocked(db, Kind(kind)); err != nil {
			return err
		}
		if _, err := db.Exec("UPDATE memories SET quarantined = 0 WHERE id = ?", id); err != nil {
			return err
		}
		logEvent(db, id, EvAccepted, text, 0)
		// Now that it is active, it belongs in the vault the same as anything
		// else — this is the moment it moves out of the review queue and into
		// memory. Both files change, and the memory file is written first: a
		// crash between the two leaves a proposal that is already remembered
		// still listed as pending, which a second accept resolves. The other
		// order would drop it from the queue with nothing holding it.
		if err := flushLocked(db, Kind(kind)); err != nil {
			return err
		}
		return flushPending(db)
	})
}

// Reject discards a quarantined memory outright. Unlike Forget, this only
// works on something still pending: rejecting an already-active memory is not
// what this call means, and forcing the caller through Forget for that keeps
// the two operations from being silently interchangeable.
func Reject(db *sql.DB, id int64) error {
	var text string
	var quarantined int
	if err := db.QueryRow("SELECT text, quarantined FROM memories WHERE id = ?", id).Scan(&text, &quarantined); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no memory #%d", id)
		}
		return err
	}
	if quarantined == 0 {
		return fmt.Errorf("memory #%d is not pending review", id)
	}
	// The delete and the rewrite are one operation, under the queue's lock: the
	// rewrite regenerates the file from the queue, so a second rejection landing
	// between them would write a file that still lists this one.
	return withPending(db, func(dir string) error {
		if err := rejectRow(db, id, text); err != nil {
			return err
		}
		// The rejection has to reach the queue file too. It is the only record of
		// what is pending that survives deleting the cache, so a proposal left
		// there would come back on the next `brain index` — a memory the user
		// explicitly rejected, reappearing, which is the failure mode Reconcile
		// exists to prevent for active memories.
		return flushPendingLocked(db, dir)
	})
}

// rejectRow is Reject's row work without the file write, for the one caller
// that is already holding the queue's lock and writing the file itself.
//
// ImportPending is that caller, and it needs this rather than Reject for a
// concrete reason: Reject flushes, flushing takes the queue lock, and
// ImportPending holds it — so calling Reject from there wedges the process
// against itself on the first proposal the user deleted by hand.
func rejectRow(db *sql.DB, id int64, text string) error {
	if _, err := db.Exec("DELETE FROM memories WHERE id = ?", id); err != nil {
		return err
	}
	logEvent(db, id, EvRejected, text, 0)
	return nil
}
