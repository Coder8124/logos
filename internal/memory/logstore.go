package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// The timeline is durable too.
//
// memory_log is the sixth thing that only .brain/index.db knew, after memories,
// working notes, proposals, the review queue and open loops. `brain memory log`
// and `brain memory diff` read it, and the rebuild every document in this
// project calls safe deleted it outright — then the memories coming back under
// their old ids logged a fresh "created" line apiece, stamped with the moment of
// the rebuild. So the history did not merely vanish, it was replaced by a
// confident and wrong one: every fact you had ever learned, reported as learned
// today, with the events that had no row left to recreate them (a forgetting, a
// rejected proposal) gone without trace.
//
// A record of what changed that cannot survive a cache rebuild is not a record.
// This writes it to memories/log.md — beside pending.md, inside the memory
// directory the note walk skips wholesale, under a filename that is not one of
// the kinds Import reads, so nothing that feeds Recall or a context pack can
// see it.

// LogFile is the timeline's name inside the memory directory. Not one of the
// kinds, for the reason PendingFile is not: Import iterates `kinds`, so this
// file is invisible to the path that decides what counts as known.
const LogFile = "log.md"

func logPath(dir string) string { return filepath.Join(dir, Dir, LogFile) }

// appendLog writes one event to the vault, and is called after the row that
// describes it is already committed.
//
// Appended rather than rewritten from the table, which is what pending.md does.
// The queue is a set that shrinks and has to be restated in full; this is an
// append-only log that only ever grows, and rewriting all of it on every
// mutation makes the cost of remembering one more thing proportional to
// everything already remembered.
func appendLog(db *sql.DB, e LogEntry) {
	dir := vaultFor(db)
	if dir == "" {
		return // an unbound store; see withPending
	}
	g, err := vault.Lock(dir, "memory-log")
	if err != nil {
		warnLog(err)
		return
	}
	defer g.Unlock()
	if err := appendLogLocked(dir, e); err != nil {
		warnLog(err)
	}
}

func appendLogLocked(dir string, e LogEntry) error {
	path := logPath(dir)
	// The memory directory need not exist yet: the first thing that ever happens
	// to a fresh vault can be a remember, and a timeline that refused to record
	// it would be missing exactly the event that starts the history.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		if _, err := f.WriteString(logHeader); err != nil {
			return err
		}
	}
	_, err = f.WriteString(renderLogLine(e))
	return err
}

// warnLog reports on the same terms logEventIn does: never silent, never fatal.
// By the time this runs the memory itself is already in the vault, and failing
// the caller here would cost the user a memory to protect a note about it.
func warnLog(err error) {
	fmt.Fprintln(os.Stderr, "· the memory timeline could not be written to the vault:", err)
}

const logHeader = "---\ntype: memory-timeline\n---\n\n" +
	"Every change to what brain knows, oldest first. This file is the record,\n" +
	"not the database: `brain index` rebuilds the timeline from these lines.\n\n" +
	"Deleting a line here deletes that event from the history. Nothing else reads\n" +
	"it — no memory is recalled or packed into context from this file.\n\n"

func renderLogLine(e LogEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s <!-- brain ts=%s id=%d ev=%s",
		oneLine(e.Detail), time.Unix(e.TS, 0).UTC().Format(time.RFC3339), e.MemID, e.Event)
	if e.RefID != 0 {
		fmt.Fprintf(&b, " ref=%d", e.RefID)
	}
	if e.Project != "" {
		fmt.Fprintf(&b, " project=%s", logField(e.Project))
	}
	b.WriteString(" -->\n")
	return b.String()
}

// logField and unLogField keep a value that contains a space or a `>` from
// ending the comment it is written inside, reversibly. The lossy alternative
// already in this codebase — replacing spaces with hyphens on the way out —
// cannot be undone on the way back in, so a project called "billing api" came
// home as "billing-api" and every rebuild was another chance to corrupt it.
func logField(s string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", ">", "%3E", "\t", "%09")
	return r.Replace(s)
}

func unLogField(s string) string {
	r := strings.NewReplacer("%20", " ", "%3E", ">", "%09", "\t", "%25", "%")
	return r.Replace(s)
}

// ImportLog restores the timeline from the vault, and is what makes deleting the
// cache survivable for the history as well as for the memories themselves.
//
// Runs after Import, so a memory the log refers to is already back in the table.
// Events are matched on what they are rather than on a row id — a log id is
// local to the database that issued it and means nothing after a wipe — so
// re-running this is a no-op rather than a second copy of every line.
//
// Returns how many events it put back.
func ImportLog(db *sql.DB, dir string) (int, error) {
	if err := Init(db); err != nil {
		return 0, err
	}
	g, err := vault.Lock(dir, "memory-log")
	if err != nil {
		return 0, err
	}
	defer g.Unlock()

	raw, err := os.ReadFile(logPath(dir))
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	restored := 0
	for _, e := range parseLog(string(raw)) {
		added, err := insertLogEntry(db, e)
		if err != nil {
			return restored, err
		}
		if added {
			restored++
		}
	}

	// Back-fill. A vault written before this file existed has memories and no
	// timeline, and the honest creation date for each of them is the one the
	// memory itself carries — not the moment of whichever rebuild happened to
	// notice. Without this, every install that predates the feature reports its
	// whole history as having happened today, which is the exact failure the
	// file was added to stop.
	backfilled, err := backfillCreations(db, dir)
	if err != nil {
		return restored, err
	}
	return restored + backfilled, nil
}

// backfillCreations gives every memory with no beginning in the log one, taken
// from the memory's own `created`. Writes them to the file too, so the repair
// happens once rather than on every rebuild.
func backfillCreations(db *sql.DB, dir string) (int, error) {
	rows, err := db.Query(`
		SELECT m.id, m.text, m.created, m.project, m.quarantined FROM memories m
		WHERE NOT EXISTS (
			SELECT 1 FROM memory_log l
			WHERE l.mem_id = m.id AND l.event IN (?, ?, ?))
		ORDER BY m.created, m.id`, EvCreated, EvQuarantined, EvAccepted)
	if err != nil {
		return 0, err
	}
	var missing []LogEntry
	for rows.Next() {
		var e LogEntry
		var quarantined int
		if err := rows.Scan(&e.MemID, &e.Detail, &e.TS, &e.Project, &quarantined); err != nil {
			rows.Close()
			return 0, err
		}
		// A proposal nobody has accepted was never created in the sense the
		// timeline means; it was queued, and saying otherwise would show an
		// un-reviewed memory as part of what brain knows.
		e.Event = EvCreated
		if quarantined == 1 {
			e.Event = EvQuarantined
		}
		missing = append(missing, e)
	}
	rows.Close()

	for _, e := range missing {
		if _, err := insertLogEntry(db, e); err != nil {
			return 0, err
		}
		if err := appendLogLocked(dir, e); err != nil {
			return 0, err
		}
	}
	return len(missing), nil
}

// insertLogEntry writes one event unless the same event is already recorded.
// The identity of an event is when it happened, to what, and what it was —
// there is no durable id to match on, and two genuinely distinct events with
// all four of those equal are indistinguishable to a reader anyway.
func insertLogEntry(db *sql.DB, e LogEntry) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM memory_log WHERE ts = ? AND mem_id = ? AND event = ? AND ref_id = ?`,
		e.TS, e.MemID, e.Event, e.RefID).Scan(&n)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err = db.Exec(
		`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id, project) VALUES (?,?,?,?,?,?)`,
		e.TS, e.MemID, e.Event, e.Detail, e.RefID, e.Project)
	return err == nil, err
}

// parseLog reads the file back. A line it cannot understand is skipped rather
// than failing the import: the timeline is a record of things that already
// happened, and refusing to restore the ninety lines that are intact because
// one was hand-edited into nonsense loses more history than it protects.
func parseLog(raw string) []LogEntry {
	var out []LogEntry
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		// LastIndex, not Index: the detail is free text an agent wrote and may
		// itself contain "<!--", and brain's own comment is always the last
		// thing on the line. Splitting at the first one truncates the event's
		// text at whatever the memory happened to quote.
		i := strings.LastIndex(line, "<!--")
		if i < 0 {
			continue
		}
		detail := strings.TrimSpace(line[2:i])
		meta := strings.TrimSuffix(strings.TrimSpace(line[i+len("<!--"):]), "-->")
		e := LogEntry{Detail: detail}
		for _, f := range strings.Fields(meta) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "ts":
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					e.TS = t.Unix()
				}
			case "id":
				e.MemID, _ = strconv.ParseInt(v, 10, 64)
			case "ev":
				e.Event = v
			case "ref":
				e.RefID, _ = strconv.ParseInt(v, 10, 64)
			case "project":
				e.Project = unLogField(v)
			}
		}
		if e.TS == 0 || e.MemID == 0 || e.Event == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}
