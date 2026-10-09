package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/vault"
)

// The review queue is durable too.
//
// Quarantine deliberately keeps an agent's proposal out of memories/<kind>.md —
// that is the consent guarantee, and ExportKind enforces it with
// `quarantined = 0`. The consequence nobody wrote down is that a pending
// proposal then lived in exactly one place: .logos/index.db. And every document
// this project ships says the same thing about that file — it is a cache,
// delete it, run `logos index`, lose nothing.
//
// You lost the queue. An agent proposes two facts, the user does not get to
// `logos review` before their next reindex, and `rm -rf .logos && logos index`
// takes both away without a word; `logos doctor` then reports "nothing pending",
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

// pendingStamps holds pending.md as each store last wrote it. See vault.Stamps.
var pendingStamps vault.Stamps

// reconcilePendingLocked adopts hand edits to the queue file, with the queue
// lock held, and runs before anything that changes the queue and rewrites it.
//
// The file tells the user that deleting a line rejects the proposal. Without
// this, that only held if `logos index` ran first: the next proposal to arrive
// regenerated the file from the cache, put the deleted line back, and the
// proposal the user had rejected was waiting for review again. It has to run
// before the change, not after — once a new proposal is in the cache, nothing
// can tell it apart from a line the user deleted.
//
// It returns how many proposals the file put back and how many it rejected.
// The writers drop the counts: a command that announces the adoption calls
// ReconcilePending first, and the writer's pass then finds the file ours.
func reconcilePendingLocked(db *sql.DB, dir string) (restored, rejected int, err error) {
	if dir == "" {
		return 0, 0, nil
	}
	raw, err := os.ReadFile(pendingPath(dir))
	if os.IsNotExist(err) {
		return 0, 0, nil // says nothing about what is pending; see ImportPending
	}
	if err != nil {
		return 0, 0, err
	}
	if pendingStamps.Ours(db, raw) {
		return 0, 0, nil
	}
	if looksTruncated(string(raw)) {
		// ImportPending refuses this file and says so on `logos index`. Here it
		// is a reason not to adopt it, not to fail the write: the rewrite that
		// follows replaces the torn file with the cache's complete queue.
		return 0, 0, nil
	}
	if restored, rejected, err = adoptPendingLocked(db, raw); err != nil {
		return restored, rejected, err
	}
	pendingStamps.Adopted(db, raw)
	return restored, rejected, nil
}

// ReconcilePending adopts hand edits to the review queue file now, and reports
// how many proposals the file put back and how many it rejected.
//
// Exported for `logos review`. Only writes used to reconcile, so the review
// offered a proposal the user had already rejected by deleting its line — and
// a write that did adopt the deletion said nothing about it.
func ReconcilePending(db *sql.DB) (restored, rejected int, err error) {
	err = withPending(db, func(dir string) error {
		restored, rejected, err = reconcilePendingLocked(db, dir)
		return err
	})
	return restored, rejected, err
}

// flushPendingLocked reads the queue and writes the file with the lock held, and
// the two have to be under the same lock for the same reason the memory files
// do. Unserialised, two proposals arriving at once read the queue, both write
// the whole file, and the later write wins with a snapshot taken before the
// other proposal existed. ImportPending then treats the id it cannot find as a
// line the user deleted and calls Reject on it — a proposal discarded on the
// user's behalf, on the next `logos index`, without them ever seeing it.
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
		pendingStamps.Forget(db)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("review queue emptied in the cache but not in the vault: %w", err)
		}
		return nil
	}
	if err := vault.WriteAtomic(path, []byte(renderPending(pend))); err != nil {
		// Whatever is on disk now is not what we last recorded writing.
		pendingStamps.Forget(db)
		if merr := markPendingUnflushed(db, path); merr != nil {
			return fmt.Errorf("proposal saved to the cache but not to the vault: %w "+
				"(and could not mark it for the next write, which may now delete it: %v)", err, merr)
		}
		return fmt.Errorf("proposal saved to the cache but not to the vault: %w", err)
	}
	// The whole queue is on disk now, so whatever an earlier failure stranded
	// went out with it. quarantined = 1 because the active memories' mark
	// describes their own files, which this write never touched.
	db.Exec("UPDATE memories SET unflushed = 0 WHERE quarantined = 1 AND unflushed = 1")
	pendingStamps.Record(db, path)
	return nil
}

// markPendingUnflushed flags the queued proposals the cache holds and the
// queue file on disk does not, after a write of that file failed. The same
// column markUnflushed uses for active memories, scoped by quarantined: for a
// proposal, the file it belongs in is this one.
//
// Persistent for the reason markUnflushed gives: the next command starts with
// no stamp, adopts the file, and without the mark would reject the stranded
// proposal as a line the user deleted — a decision logged in their name on a
// proposal they never saw.
//
// Its error is returned, not dropped: the mark is the only thing between the
// stranded row and the next write's adopt, so a mark that did not land is a
// second failure the caller must hear about. The caller appends it to the
// write's own error rather than replacing it.
func markPendingUnflushed(db *sql.DB, path string) error {
	onDisk := map[int64]bool{}
	if raw, err := os.ReadFile(path); err == nil {
		for _, m := range parseKind(Fact, string(raw)) {
			onDisk[m.ID] = true
		}
	}
	rows, err := db.Query("SELECT id FROM memories WHERE quarantined = 1 AND superseded = 0")
	if err != nil {
		return err
	}
	var stranded []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil && !onDisk[id] {
			stranded = append(stranded, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range stranded {
		if _, err := db.Exec("UPDATE memories SET unflushed = 1 WHERE id = ?", id); err != nil {
			return err
		}
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
	b.WriteString("Run `logos review` to accept or reject them. Deleting a line here\n" +
		"rejects it, from the next proposal logos queues or reviews, or the next\n" +
		"`logos index`, whichever is first; this file is the record, not the database.\n\n")
	for _, m := range pend {
		fmt.Fprintf(&b, "- %s <!-- logos id=%d kind=%s conf=%.2f sal=%.2f src=%s created=%s uses=%d",
			oneLine(m.Text), m.ID, m.Kind, m.Confidence, m.Salience, orDash(m.Source),
			time.Unix(m.Created, 0).UTC().Format(time.RFC3339), m.Uses)
		if m.Project != "" {
			fmt.Fprintf(&b, " project=%s", logField(m.Project))
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
//
// Returns how many proposals it put back into the cache, and how many it
// rescued the other way — out of a cache that was their only copy and into the
// vault. They are separate numbers because they are opposite events, and a
// caller that reported a rescue as a restore would tell the user their queue had
// been recovered on a run where it was merely, finally, written down.
func ImportPending(db *sql.DB, dir string) (int, int, error) {
	if err := Init(db); err != nil {
		return 0, 0, err
	}
	// The same lock the writers take. This reads the file and then rejects every
	// queued row missing from it, which is the destructive half — running it
	// against a file another process is part-way through writing rejects
	// proposals that are only briefly absent.
	//
	// dir is the caller's rather than vaultFor's: `logos index` imports a vault
	// it has not bound to a store yet.
	g, err := vault.Lock(dir, "memory-pending")
	if err != nil {
		return 0, 0, err
	}
	defer g.Unlock()

	raw, err := os.ReadFile(pendingPath(dir))
	if os.IsNotExist(err) {
		// No file is not the same as an empty queue: a vault written before this
		// existed, or a partial restore, says nothing about what is pending. The
		// rows in the cache stand — but "the next mutation writes the file" was
		// not a plan, it was a hope. Nothing mutates a queue nobody is reviewing,
		// so a vault older than this file kept its proposals in the one place
		// every document in this project tells the user they may delete, and the
		// next `rm -rf .logos` took them without a word. This is the only moment
		// the rescue is still possible: the cache is still the only copy.
		rescued, err := rescuePendingLocked(db, dir)
		return 0, rescued, err
	}
	if err != nil {
		return 0, 0, err
	}
	if looksTruncated(string(raw)) {
		return 0, 0, fmt.Errorf(
			"refusing to import an incomplete review queue (%s ends mid-record); "+
				"restore the file or delete the partial line to accept it as-is", PendingFile)
	}

	restored, _, err := adoptPendingLocked(db, raw)
	if err != nil {
		return restored, 0, err
	}
	pendingStamps.Adopted(db, raw)
	// Proposals a failed write left only in the cache, which the adopt kept.
	// Written out now, for the reason the absent-file branch gives.
	var rescued int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE quarantined = 1 AND superseded = 0 AND unflushed = 1").Scan(&rescued); err != nil {
		return restored, 0, err
	}
	if rescued > 0 {
		if err := flushPendingLocked(db, dir); err != nil {
			return restored, 0, err
		}
	}
	return restored, rescued, nil
}

// adoptPendingLocked makes the queue in the cache match the file: every line is
// restored, and every queued row with no line is rejected. Returns how many
// proposals it had to put back and how many it rejected.
func adoptPendingLocked(db *sql.DB, raw []byte) (restored, rejected int, err error) {
	parsed := parseKind(Fact, string(raw)) // kind= in each record overrides this default
	keep := map[int64]bool{}
	for _, m := range parsed {
		id, created, err := upsertPending(db, m)
		if err != nil {
			return restored, rejected, err
		}
		keep[id] = true
		if created {
			restored++
		}
	}

	// A line the user deleted is a rejection. Only rows this file could have
	// described are eligible — an active memory is not in the queue and must
	// never be reaped by it.
	// unflushed = 0: a proposal whose write failed was never in the file, so
	// its absence there is not a rejection. See markPendingUnflushed.
	rows, err := db.Query("SELECT id, text FROM memories WHERE quarantined = 1 AND superseded = 0 AND unflushed = 0")
	if err != nil {
		return restored, rejected, err
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
			return restored, rejected, err
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
			return restored, rejected, err
		}
		rejected++
	}
	return restored, rejected, nil
}

// rescuePendingLocked writes a queue that exists only in the cache out to the
// vault, and reports how many proposals it saved.
//
// Locked, because ImportPending already holds the queue lock it would otherwise
// take — the same reason rejectRow exists rather than a call to Reject.
func rescuePendingLocked(db *sql.DB, dir string) (int, error) {
	pend, err := Pending(db)
	if err != nil {
		return 0, err
	}
	if len(pend) == 0 {
		// Nothing to rescue, and nothing to write: an absent file is what an
		// empty queue looks like. See flushPendingLocked.
		return 0, nil
	}
	if err := flushPendingLocked(db, dir); err != nil {
		return 0, err
	}
	return len(pend), nil
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
	// No event. Putting a proposal back is not proposing it, and logging one
	// stamped with time.Now() re-dated every queued fact to the moment of the
	// rebuild — the same way restoring a wiped memory used to. The honest date
	// is the one the proposal carries, and backfillCreations supplies it.
	return id, true, nil
}
