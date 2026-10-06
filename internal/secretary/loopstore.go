package secretary

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/logos/internal/vault"
)

// Open loops, written down.
//
// This is the fifth time this shape of bug has appeared — memories, working
// notes, checkpoints, the review queue, now commitments — and the cause never
// changes: state that only the database knows. Every document this project
// ships says delete .logos/index.db, run `logos index`, lose nothing. For
// `logos loop` that was false. Track a commitment, rebuild the index, and the
// list came back empty, which reads exactly like having finished everything.
// Nothing was said, because nothing knew anything had been lost.
//
// Where it lives: loops.md at the vault root, not under memories/ or
// sessions/. A loop is neither a remembered fact nor a record of a session —
// it is the one list in the vault that is about the future, and it belongs
// where a person opening the vault will see it. index.Sync skips it by name so
// it is not also indexed as a note; the loop list is not a document to search,
// it is a list to act on.
//
// Closed loops stay in the file. Dropped ones especially: the fingerprint that
// stops a re-extracted commitment from arriving twice only works if the
// dismissal is still on record, so a file holding open loops alone would let
// every dismissed loop come back the next time Extract ran.

// LoopsFile is the loop list's name at the vault root.
const LoopsFile = "loops.md"

// LoopsPath is where the loop list lives inside dir. Exported because
// index.Sync has to know the one file in the vault root it must not index.
func LoopsPath(dir string) string { return filepath.Join(dir, LoopsFile) }

// The vault a store writes through to, keyed by the database it belongs to.
// Same shape and same reason as internal/memory's registry: a process can hold
// two vaults at once, and one package-level path would let the second bind
// silently redirect the first vault's writes into the second's directory.
var (
	vaultMu sync.RWMutex
	vaults  = map[*sql.DB]string{}
)

// SetVault binds a database to the vault its loops belong in. An empty dir
// unbinds it, which is what a cache-only store wants and what index.Close does
// so a closed handle leaves nothing behind.
func SetVault(db *sql.DB, dir string) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	stamps.Forget(db)
	if dir == "" {
		delete(vaults, db)
		return
	}
	vaults[db] = dir
}

func vaultFor(db *sql.DB) string {
	vaultMu.RLock()
	defer vaultMu.RUnlock()
	return vaults[db]
}

// stamps holds loops.md as each store last wrote it. See vault.Stamps.
var stamps vault.Stamps

// withLoops runs a mutation holding the loop list's lock, after adopting
// whatever the user changed in the file by hand, and passes fn the vault dir
// ("" for a cache-only store) so it can flushLocked when it is done.
//
// One lock across reconcile, mutation and write, for two reasons. Unserialised,
// two loops added at the same moment both read the list and both write the
// file, and the later write wins with a snapshot taken before the other loop
// existed; Import would then take the missing row for a deletion and remove a
// commitment the user never dismissed. And the reconcile has to come before the
// mutation: once a new row is in the cache, nothing can tell it apart from a
// line the user deleted.
//
// Without the reconcile, the file's own promise — delete a line and the loop is
// forgotten — only held if `logos index` ran before the next `logos loop add`,
// which regenerated the file from the cache and put the line back.
func withLoops(db *sql.DB, fn func(dir string) error) error {
	dir := vaultFor(db)
	if dir == "" {
		return fn("") // a cache-only store — tests, and handles index.Close unbound
	}
	g, err := vault.Lock(dir, "loops")
	if err != nil {
		return err
	}
	defer g.Unlock()
	// Returned rather than written past: rewriting the file over an edit we
	// could not adopt is the thing this exists to prevent. The counts are
	// dropped here, not lost: a command that wants to announce the adoption
	// calls Reconcile first, and this pass then finds the file stamped as ours.
	// What it still catches is an edit made between the two.
	if _, _, err := reconcileLocked(db, dir); err != nil {
		return err
	}
	return fn(dir)
}

// Reconcile adopts hand edits to loops.md now, and reports how many loops the
// file put back and how many it forgot.
//
// Exported for the commands that read or change the list. Only writes used to
// reconcile, so `logos loop` listed a loop the user had deleted from the file —
// at exactly the moment they ran it to check that the deletion took — and a
// write that did adopt the edit said nothing about it. A file we wrote is one
// read and a hash compare, so calling this before every list is cheap.
func Reconcile(db *sql.DB) (restored, removed int, err error) {
	dir := vaultFor(db)
	if dir == "" {
		return 0, 0, nil
	}
	g, err := vault.Lock(dir, "loops")
	if err != nil {
		return 0, 0, err
	}
	defer g.Unlock()
	return reconcileLocked(db, dir)
}

// reconcileLocked adopts hand edits to loops.md, with the lock held. A file we
// wrote ourselves is skipped on its hash; an absent file says nothing about
// what is open, as Import explains.
func reconcileLocked(db *sql.DB, dir string) (restored, removed int, err error) {
	raw, err := os.ReadFile(LoopsPath(dir))
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if stamps.Ours(db, raw) {
		return 0, 0, nil
	}
	if looksTruncated(string(raw)) {
		// Import refuses this file and says so when the user runs `logos
		// index`. Here it is a reason not to adopt the file, not to fail an
		// unrelated `loop add`: the rewrite that follows replaces the torn
		// file with the cache's complete copy, which is the repair.
		return 0, 0, nil
	}
	if restored, removed, err = adoptLocked(db, raw); err != nil {
		return restored, removed, err
	}
	stamps.Adopted(db, raw)
	return restored, removed, nil
}

// flushLocked rewrites the whole file from the database, with the lock held.
// Whole-file because a loop list is current state rather than a log: one
// successful write heals whatever a failed one left behind.
func flushLocked(db *sql.DB, dir string) error {
	if dir == "" {
		return nil
	}
	all, err := allLoops(db)
	if err != nil {
		return err
	}
	path := LoopsPath(dir)
	if len(all) == 0 {
		// No loops is an absent file, not an empty one. A person who has never
		// tracked a loop should not find a page in their vault about it.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			stamps.Forget(db)
			return fmt.Errorf("loops emptied in the cache but not in the vault: %w", err)
		}
		stamps.Forget(db)
		return nil
	}
	if err := vault.WriteAtomic(path, []byte(render(all))); err != nil {
		// Forgotten, not kept: whatever is on disk now is not what we last
		// recorded writing, and a stale stamp would skip adopting it.
		stamps.Forget(db)
		markUnflushed(db, path)
		return fmt.Errorf("loop saved to the cache but not to the vault: %w", err)
	}
	// The whole list is on disk now, so whatever an earlier failure stranded
	// went out with it.
	db.Exec("UPDATE commitments SET unflushed = 0 WHERE unflushed = 1")
	stamps.Record(db, path)
	return nil
}

// markUnflushed flags the loops the cache holds and the file on disk does not,
// after a write of that file failed.
//
// Persistent rather than a stamp kept in memory, because every CLI command is
// its own process: the next `loop add` starts with no stamp, adopts the file,
// and without the mark would delete the stranded loop as a line the user had
// removed — right before the write that would have carried it out. Measured
// against the bytes on disk, since a failed atomic write leaves the previous
// file intact and every loop in it is still durable.
//
// Errors are dropped: this runs on the failure path of a write that is already
// returning the error that says what went wrong.
func markUnflushed(db *sql.DB, path string) {
	onDisk := map[int64]bool{}
	if raw, err := os.ReadFile(path); err == nil {
		for _, c := range parse(string(raw)) {
			onDisk[c.ID] = true
		}
	}
	rows, err := db.Query("SELECT id FROM commitments")
	if err != nil {
		return
	}
	var stranded []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil && !onDisk[id] {
			stranded = append(stranded, id)
		}
	}
	rows.Close()
	for _, id := range stranded {
		db.Exec("UPDATE commitments SET unflushed = 1 WHERE id = ?", id)
	}
}

// allLoops reads every commitment, open and closed, oldest first — the file is
// the whole record, not the open subset.
func allLoops(db *sql.DB) ([]Commitment, error) {
	rows, err := db.Query(
		`SELECT id, text, who, created, due_hint, status, source_ref, resolved_at
		 FROM commitments ORDER BY created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Commitment
	for rows.Next() {
		var c Commitment
		var who, due, src sql.NullString
		var st string
		if err := rows.Scan(&c.ID, &c.Text, &who, &c.Created, &due, &st, &src, &c.ResolvedAt); err != nil {
			return nil, err
		}
		c.Who, c.DueHint, c.SourceRef, c.Status = who.String, due.String, src.String, Status(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

// render writes the list the way a person would want to read it, and says what
// they can do about it. Checkboxes because that is what every markdown editor
// already draws; the bookkeeping rides in an HTML comment, which Obsidian does
// not render, so the page reads as a plain to-do list and still round-trips.
func render(all []Commitment) string {
	open := 0
	for _, c := range all {
		if c.Status == Open {
			open++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: open-loops\nopen: %d\n---\n\n", open)
	b.WriteString("Things you said you would do. `logos loop` lists what is still open;\n" +
		"`logos loop done <id>` or `logos loop drop <id>` closes one.\n\n")
	b.WriteString("Ticking a box here does nothing on its own — the status in each line's\n" +
		"comment is what counts. Deleting a line forgets that loop entirely, from the\n" +
		"next loop logos adds or closes, or the next `logos index`, whichever is first;\n" +
		"this file is the record, not the database.\n\n")
	for _, c := range all {
		box := " "
		switch c.Status {
		case Done:
			box = "x"
		case Dropped:
			box = "-"
		}
		fmt.Fprintf(&b, "- [%s] %s <!-- logos id=%d status=%s created=%s",
			box, oneLine(c.Text), c.ID, c.Status, stamp(c.Created))
		if c.Who != "" {
			fmt.Fprintf(&b, " who=%s", field(c.Who))
		}
		if c.DueHint != "" {
			fmt.Fprintf(&b, " due=%s", field(c.DueHint))
		}
		if c.SourceRef != "" {
			fmt.Fprintf(&b, " src=%s", field(c.SourceRef))
		}
		if c.ResolvedAt != 0 {
			fmt.Fprintf(&b, " resolved=%s", stamp(c.ResolvedAt))
		}
		b.WriteString(" -->\n")
	}
	return b.String()
}

func stamp(sec int64) string { return time.Unix(sec, 0).UTC().Format(time.RFC3339) }

// oneLine keeps a record to a single bullet. A newline in the text would split
// one loop into a line the parser reads and a line it silently drops.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// field makes a value safe to sit in a space-separated metadata comment, and
// safe to close it: a "-->" inside who= or src= would end the comment early and
// spill bookkeeping into the rendered page.
//
// Percent-encoding rather than substitution, because loops.md is the record and
// the cache is rebuilt from it — so the encoding has to be reversible. Writing a
// space as "-" was not: unfield turned every "-" back into a space, which made
// "Jean-Luc" into "Jean Luc", "2026-09-12" into "2026 09 12" and a dated source
// ref into nonsense. It was progressive, too — the corrupted value was written
// back on the next flush, and since who feeds the fingerprint, a re-extracted
// loop stopped deduping against its own corrupted row and arrived twice.
func field(s string) string {
	var b strings.Builder
	for _, r := range oneLine(s) {
		switch r {
		// Space ends a field, ">" is the only character that can close the
		// comment, and "%" has to escape itself for any of it to be reversible.
		case '%':
			b.WriteString("%25")
		case ' ':
			b.WriteString("%20")
		case '>':
			b.WriteString("%3E")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unfield is field's inverse. Unknown escapes are left alone rather than
// dropped: a value hand-typed into the file is still the user's record.
func unfield(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			switch strings.ToUpper(s[i : i+3]) {
			case "%25":
				b.WriteByte('%')
				i += 3
				continue
			case "%20":
				b.WriteByte(' ')
				i += 3
				continue
			case "%3E":
				b.WriteByte('>')
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Import restores loops from the vault, and is what makes deleting the cache
// survivable for commitments.
//
// dir is the caller's rather than vaultFor's: `logos index` imports a vault it
// has not bound to a store yet.
//
// Returns how many loops it had to put back — a row already in the cache was
// never lost and is not announced as recovered — and how many it rescued the
// other way, out of a cache that was their only copy and into the file.
func Import(db *sql.DB, dir string) (restored, rescued int, err error) {
	if err := Init(db); err != nil {
		return 0, 0, err
	}
	// The same lock the writers take. The destructive half below reads the file
	// and then removes every row missing from it; run against a file another
	// process is part-way through writing, it would forget loops that are only
	// briefly absent.
	g, err := vault.Lock(dir, "loops")
	if err != nil {
		return 0, 0, err
	}
	defer g.Unlock()

	raw, err := os.ReadFile(LoopsPath(dir))
	if os.IsNotExist(err) {
		// Absent is not empty: a vault written before loops were durable, or a
		// partial restore, says nothing about what is open. The rows in the
		// cache stand — and are written down now rather than at the next
		// mutation, because a vault that has loops only in its cache is one
		// rebuild away from losing them, and this is the command people run
		// right before that happens.
		if err := db.QueryRow("SELECT COUNT(*) FROM commitments").Scan(&rescued); err != nil {
			return 0, 0, err
		}
		if err := flushLocked(db, dir); err != nil {
			return 0, 0, err
		}
		return 0, rescued, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if looksTruncated(string(raw)) {
		return 0, 0, fmt.Errorf(
			"refusing to import an incomplete loop list (%s ends mid-record); "+
				"restore the file or delete the partial line to accept it as-is", LoopsFile)
	}

	restored, _, err = adoptLocked(db, raw)
	if err != nil {
		return restored, 0, err
	}
	stamps.Adopted(db, raw)
	// Loops a failed write left only in the cache, which adoptLocked kept. This
	// is the command people run right before deleting that cache, so they go
	// out now rather than at the next mutation, and the count says so.
	if err := db.QueryRow("SELECT COUNT(*) FROM commitments WHERE unflushed = 1").Scan(&rescued); err != nil {
		return restored, 0, err
	}
	if rescued > 0 {
		if err := flushLocked(db, dir); err != nil {
			return restored, 0, err
		}
	}
	return restored, rescued, nil
}

// adoptLocked makes the cache match the file: every line is upserted at its id,
// and every row with no line is deleted. Returns how many rows it had to create
// and how many it deleted — both, because invariant 3 asks for the number, and
// a deletion is the half a user is most likely to want confirmed.
func adoptLocked(db *sql.DB, raw []byte) (restored, removed int, err error) {
	parsed := parse(string(raw))
	keep := map[int64]bool{}
	// The file tells the user a line is theirs to delete, which makes a
	// copy-pasted line an expected edit rather than an exotic one. Two lines
	// with the same text collide on the fingerprint UNIQUE index, which
	// ON CONFLICT(id) does not cover — and the error came back from `logos
	// index` after some rows had been upserted and before the reconciling
	// delete pass below, leaving the cache half-imported. A repeated commitment
	// is one commitment. The next flush rewrites the file without the duplicate,
	// so the user sees which line won.
	seen := map[string]bool{}
	for _, c := range parsed {
		fp := fingerprint(c.Text, c.Who)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		created, err := upsert(db, c)
		if err != nil {
			return restored, removed, err
		}
		keep[c.ID] = true
		if created {
			restored++
		}
	}

	// A line the user deleted is a loop they no longer want on the record. The
	// row goes with it — unlike a dropped loop, which they asked to keep as
	// dismissed, a deleted line has no representation left in the vault, and
	// leaving the row would put the text back in the file on the next flush.
	// unflushed = 0: a loop whose write failed was never in the file, so its
	// absence there is not a deletion. See markUnflushed.
	rows, err := db.Query("SELECT id FROM commitments WHERE unflushed = 0")
	if err != nil {
		return restored, removed, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return restored, removed, err
		}
		if !keep[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := db.Exec("DELETE FROM commitments WHERE id = ?", id); err != nil {
			return restored, removed, err
		}
		removed++
	}
	return restored, removed, nil
}

// upsert restores one loop at the id the file gives it, so every reference to
// that id — a checkpoint saying "closed loop 4", a person typing `logos loop
// done 4` — still means the same thing after a rebuild. Reports whether the row
// had to be created.
func upsert(db *sql.DB, c Commitment) (bool, error) {
	var exists int
	err := db.QueryRow("SELECT COUNT(*) FROM commitments WHERE id = ?", c.ID).Scan(&exists)
	if err != nil {
		return false, err
	}
	_, err = db.Exec(
		`INSERT INTO commitments (id, text, who, created, due_hint, status, source_ref, fingerprint, resolved_at)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET text=excluded.text, who=excluded.who, created=excluded.created,
		   due_hint=excluded.due_hint, status=excluded.status, source_ref=excluded.source_ref,
		   fingerprint=excluded.fingerprint, resolved_at=excluded.resolved_at`,
		c.ID, c.Text, c.Who, c.Created, c.DueHint, string(c.Status), c.SourceRef,
		fingerprint(c.Text, c.Who), c.ResolvedAt)
	if err != nil {
		return false, err
	}
	return exists == 0, nil
}

// parse reads the records back. A record with no id= is skipped rather than
// given a fresh one: an id is how the rest of the system names a loop, and
// inventing one for a line somebody hand-wrote would create a second loop every
// time the file was read.
func parse(raw string) []Commitment {
	var out []Commitment
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		// Last, not first. logos's bookkeeping is always the final comment on the
		// line, and a loop whose own text mentions "<!--" — "strip the <!-- hack
		// --> from the page" — was otherwise cut off at the word before it, on
		// every import, with no error.
		i := strings.LastIndex(line, "<!--")
		j := strings.LastIndex(line, "-->")
		if i < 0 || j < i {
			continue
		}
		text := strings.TrimSpace(line[:i])
		if _, after, ok := strings.Cut(text, "]"); ok {
			text = strings.TrimSpace(after)
		}
		c := Commitment{Text: text, Status: Open}
		for _, f := range strings.Fields(line[i+4 : j]) {
			key, value, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			value = unfield(value)
			switch key {
			case "id":
				c.ID, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			case "status":
				c.Status = Status(strings.TrimSpace(value))
			case "created":
				c.Created = parseStamp(f)
			case "resolved":
				c.ResolvedAt = parseStamp(f)
			case "who":
				c.Who = value
			case "due":
				c.DueHint = value
			case "src":
				c.SourceRef = value
			}
		}
		if c.ID == 0 || c.Text == "" {
			continue
		}
		if c.Created == 0 {
			c.Created = time.Now().Unix()
		}
		out = append(out, c)
	}
	return out
}

// parseStamp takes the whole "key=value" field because an RFC3339 timestamp
// contains its own '-' separators, which the value-level hyphen decoding above
// would otherwise turn back into spaces.
func parseStamp(field string) int64 {
	_, value, _ := strings.Cut(field, "=")
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return t.Unix()
}

// looksTruncated reports a file that ends mid-record — the shape a write
// interrupted part-way through leaves, and one a hand edit never produces. See
// internal/memory's copy for the full argument; the narrow gap it admits (a
// tear landing exactly on a line boundary) is the same here, and logos's own
// writes go through vault.WriteAtomic, which replaces by rename and cannot
// tear.
func looksTruncated(raw string) bool {
	trimmed := strings.TrimRight(raw, " \t\r\n")
	if trimmed == "" {
		return false
	}
	if rest, ok := strings.CutPrefix(trimmed, "---"); ok && !strings.Contains(rest, "\n---") {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	return strings.HasPrefix(last, "- ") &&
		strings.Contains(last, "<!--") && !strings.Contains(last, "-->")
}
