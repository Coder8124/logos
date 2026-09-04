package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// The review queue is durable too.
//
// Quarantine deliberately keeps an agent's proposal out of memories/<kind>.md —
// that is the consent guarantee, and ExportKind enforces it with
// `quarantined = 0`. The consequence nobody wrote down is that a pending
// proposal then lived in exactly one place: .brain/index.db. And every document
// this project ships says the same thing about that file — it is a cache,
// delete it, run `brain index`, lose nothing.
//
// You lost the queue. An agent proposes two facts, the user does not get to
// `brain review` before their next reindex, and `rm -rf .brain && brain index`
// takes both away without a word; `brain doctor` then reports "nothing pending",
// which is indistinguishable from having reviewed them. This is the third time
// this shape of bug has appeared (memories, then working notes, now proposals),
// and it always has the same cause: state that only the database knows.
//
// The fix keeps both properties rather than trading one for the other. Pending
// proposals go to memories/pending.md — inside the memory directory, which the
// note walk skips wholesale (see index.Sync), and under a filename that is not
// one of the four kinds Import reads, so nothing that feeds Recall, All or a
// context pack can see it. It is a waiting room with a door on it, written down.

// PendingFile is the review queue's name inside the memory directory. Not one
// of the kinds, on purpose: Import iterates `kinds`, so this file is invisible
// to the path that decides what counts as known.
const PendingFile = "pending.md"

func pendingPath(dir string) string {
	return filepath.Join(dir, Dir, PendingFile)
}

// flushPending rewrites the queue file from the database. Called after every
// change to what is pending — a proposal arriving, being accepted, or being
// rejected — so the file is never a stale view of a queue the user is working.
//
// Whole-file, like flush: a queue is current state, not a log, and rebuilding
// it each time means one successful write heals whatever the last failed one
// left behind.
func flushPending(db *sql.DB) error {
	return withPending(db, func(dir string) error { return flushPendingLocked(db, dir) })
}

// withPending runs fn holding the review queue's lock, against every process on
// the machine. Taken *inside* the kind lock wherever both are held — Store's
// quarantined arrival, Accept — and that order is fixed, because two agents
// accepting proposals at the same moment is how a nesting inversion turns into
// a vault that never finishes a write.
func withPending(db *sql.DB, fn func(dir string) error) error {
	dir := vaultFor(db)
	// An unbound store has no queue file to serialise, but fn still has to run:
	// callers put their row work inside it, and skipping that turned Reject into
	// a no-op for every cache-only store.
	if dir == "" {
		return fn("")
	}
	g, err := vault.Lock(dir, "memory-pending")
	if err != nil {
		return err
	}
	defer g.Unlock()
	return fn(dir)
}

// flushPendingLocked reads the queue and writes the file with the lock held, and
// the two have to be under the same lock for the same reason the memory files
// do. Unserialised, two proposals arriving at once read the queue, both write
// the whole file, and the later write wins with a snapshot taken before the
// other proposal existed. ImportPending then treats the id it cannot find as a
// line the user deleted and calls Reject on it — a proposal discarded on the
// user's behalf, on the next `brain index`, without them ever seeing it.
//
// Because the read happens under the lock and every mutation flushes after
// committing, whoever holds the lock last writes the most recent queue.
func flushPendingLocked(db *sql.DB, dir string) error {
	if dir == "" {
		return nil // an unbound store; see withPending
	}
	pend, err := Pending(db)
	if err != nil {
		return err
	}
	path := pendingPath(dir)
	if len(pend) == 0 {
		// An empty queue is an absent file. A reviewer who cleared their backlog
		// should not find a file telling them they have one.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("review queue emptied in the cache but not in the vault: %w", err)
		}
		return nil
	}
	if err := vault.WriteAtomic(path, []byte(renderPending(pend))); err != nil {
		return fmt.Errorf("proposal saved to the cache but not to the vault: %w", err)
	}
	return nil
}

// renderPending writes the queue the way a person would want to read it, and
// says what the two things they can do about it are. Each record carries kind=
// because unlike the per-kind files this one holds every kind at once.
func renderPending(pend []Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: memory-review-queue\ncount: %d\n---\n\n", len(pend))
	b.WriteString("Memories an agent proposed. None of these is active: nothing here\n" +
		"is recalled, packed into context, or visible to any agent until you accept it.\n\n")
	b.WriteString("Run `brain review` to accept or reject them. Deleting a line here\n" +
		"rejects it on the next `brain index`; this file is the record, not the database.\n\n")
	for _, m := range pend {
		fmt.Fprintf(&b, "- %s <!-- brain id=%d kind=%s conf=%.2f sal=%.2f src=%s created=%s uses=%d",
			oneLine(m.Text), m.ID, m.Kind, m.Confidence, m.Salience, orDash(m.Source),
			time.Unix(m.Created, 0).UTC().Format(time.RFC3339), m.Uses)
		if m.Project != "" {
			fmt.Fprintf(&b, " project=%s", m.Project)
		}
		if m.Agent != "" {
			fmt.Fprintf(&b, " agent=%s", strings.ReplaceAll(m.Agent, " ", "-"))
		}
		b.WriteString(" -->\n")
	}
	return b.String()
}

// ImportPending restores the review queue from the vault, and is what makes
// deleting the cache survivable for proposals as well as for memories.
//
// Ordering matters: this runs after Import, so an id the queue claims is one
// Import has already had its chance to restore as an active memory. If a
// proposal was accepted between the last flush and now, the row exists and is
// no longer quarantined — re-quarantining it would un-accept a decision the
// user already made, so an existing row is left exactly as it is.
//
// A damaged file is refused rather than acted on, for the reason Import gives:
// a truncated queue read as authoritative would silently reject every proposal
// the missing tail held.
func ImportPending(db *sql.DB, dir string) (int, error) {
	if err := Init(db); err != nil {
		return 0, err
	}
	// The same lock the writers take. This reads the file and then rejects every
	// queued row missing from it, which is the destructive half — running it
	// against a file another process is part-way through writing rejects
	// proposals that are only briefly absent.
	//
	// dir is the caller's rather than vaultFor's: `brain index` imports a vault
	// it has not bound to a store yet.
	g, err := vault.Lock(dir, "memory-pending")
	if err != nil {
		return 0, err
	}
	defer g.Unlock()

	raw, err := os.ReadFile(pendingPath(dir))
	if os.IsNotExist(err) {
		// No file is not the same as an empty queue: a vault written before this
		// existed, or a partial restore, says nothing about what is pending. The
		// rows in the cache stand, and the next mutation writes the file.
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if looksTruncated(string(raw)) {
		return 0, fmt.Errorf(
			"refusing to import an incomplete review queue (%s ends mid-record); "+
				"restore the file or delete the partial line to accept it as-is", PendingFile)
	}

	parsed := parseKind(Fact, string(raw)) // kind= in each record overrides this default
	keep := map[int64]bool{}
	restored := 0
	for _, m := range parsed {
		id, created, err := upsertPending(db, m)
		if err != nil {
			return restored, err
		}
		keep[id] = true
		if created {
			restored++
		}
	}

	// A line the user deleted is a rejection. Only rows this file could have
	// described are eligible — an active memory is not in the queue and must
	// never be reaped by it.
	rows, err := db.Query("SELECT id, text FROM memories WHERE quarantined = 1 AND superseded = 0")
	if err != nil {
		return restored, err
	}
	type doomed struct {
		id   int64
		text string
	}
	var gone []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.id, &d.text); err != nil {
			rows.Close()
			return restored, err
		}
		if !keep[d.id] {
			gone = append(gone, d)
		}
	}
	rows.Close()
	for _, d := range gone {
		// rejectRow, not Reject: this already holds the queue lock that Reject's
		// flush would take, and the file it would rewrite is the one being read
		// as the source of truth right here. See rejectRow.
		if err := rejectRow(db, d.id, d.text); err != nil {
			return restored, err
		}
	}
	return restored, nil
}

// upsertPending restores one queued proposal. It reports whether the row had to
// be created, which is what "restored N proposals" counts — an id already in the
// cache was never lost and should not be announced as recovered.
//
// Unlike upsert this never re-embeds: a quarantined memory is excluded from
// every retrieval path, so a vector for it would be work done for a query that
// cannot reach it. Accept embeds it at the moment it becomes recallable.
func upsertPending(db *sql.DB, m Memory) (int64, bool, error) {
	if m.ID > 0 {
		var exists int
		if err := db.QueryRow("SELECT COUNT(*) FROM memories WHERE id = ?", m.ID).Scan(&exists); err != nil {
			return 0, false, err
		}
		if exists > 0 {
			return m.ID, false, nil
		}
	}
	id := m.ID
	if id <= 0 {
		id = nextID(db)
	}
	if _, err := db.Exec(
		`INSERT INTO memories (id, text, kind, salience, confidence, project, source, agent, created, uses, fingerprint, quarantined)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,1)`,
		id, m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project,
		m.Source, m.Agent, m.Created, m.Uses, fingerprint(m.Text)); err != nil {
		return 0, false, err
	}
	logEvent(db, id, EvQuarantined, m.Text, 0)
	return id, true, nil
}
