package dream

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/brain/internal/vault"
)

// Dreamed insights, written down.
//
// This is the seventh time this shape of bug has appeared — memories, working
// notes, checkpoints, proposals, the review queue, open loops, now this — and
// the cause never changes: state that only the database knows. Every document
// this project ships says delete .brain/index.db, run `brain index`, lose
// nothing. For `brain dream review` that was false. A REM pass proposes three
// connections, the user rebuilds the index before looking at them, and the
// queue comes back empty — which reads exactly like having reviewed them.
//
// It is the worst row in the database to lose, too: a model ran to produce it,
// and a person has not yet had their say about it.
//
// Where it lives: dream-insights.md at the vault root, beside loops.md, for the
// same reason. It is not a remembered fact and not a record of a session; it is
// a short list waiting on the user's judgement, and it belongs where a person
// opening the vault will see it. index.Sync skips it by name so it is not also
// indexed as a note — a proposal is not a document to search, and accepting one
// would otherwise silently change a note the user never wrote.
//
// Reviewed insights stay in the file, accepted and rejected alike. The
// rejection especially: "you dreamed this and I said no" is the record that
// tunes what the pass proposes next, and a file holding only pending insights
// would hand every refused connection back on the next rebuild.

// InsightsFile is the queue's name at the vault root.
const InsightsFile = "dream-insights.md"

// lockName is this queue's own advisory lock, distinct from the memory review
// queue's and the loop list's. Sharing one would serialise two unrelated files
// as though they were the same record.
const lockName = "dream-insights"

// InsightsPath is where the queue lives inside dir. Exported because index.Sync
// has to know which file in the vault root it must not index.
func InsightsPath(dir string) string { return filepath.Join(dir, InsightsFile) }

// The vault a queue writes through to, keyed by the database it belongs to.
// Same shape and same reason as internal/secretary's registry: a process can
// hold two vaults at once, and one package-level path would let the second bind
// redirect the first vault's writes into the second's directory.
var (
	vaultMu sync.RWMutex
	vaults  = map[*sql.DB]string{}
)

// SetVault binds a database to the vault its insights belong in. An empty dir
// unbinds it, which is what a cache-only store wants and what index.Close does
// so a closed handle leaves nothing behind.
func SetVault(db *sql.DB, dir string) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
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

// flush rewrites the whole file from the database, under the lock every other
// writer takes. Whole-file because the queue is current state rather than a
// log: one successful write heals whatever a failed one left behind.
//
// Unserialised, two insights queued at the same moment both read the table and
// both write the file, and the later write wins with a snapshot taken before
// the other insight existed. Import would then see a row it cannot find in the
// file, take that for a deletion, and discard a proposal the user never saw.
func flush(db *sql.DB) error {
	dir := vaultFor(db)
	if dir == "" {
		return nil // a cache-only store — tests, and handles index.Close unbound
	}
	g, err := vault.Lock(dir, lockName)
	if err != nil {
		return err
	}
	defer g.Unlock()
	return flushLocked(db, dir)
}

// flushLocked is flush with the lock already held — Import needs it to write
// the first copy of a vault that predates this file.
func flushLocked(db *sql.DB, dir string) error {
	all, err := allInsights(db)
	if err != nil {
		return err
	}
	path := InsightsPath(dir)
	if len(all) == 0 {
		// No insights is an absent file, not an empty one. Someone who has
		// never run a dream pass should not find a page in their vault about
		// a queue they do not have.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dreamed insights emptied in the cache but not in the vault: %w", err)
		}
		return nil
	}
	if err := vault.WriteAtomic(path, []byte(renderInsights(all))); err != nil {
		return fmt.Errorf("insight saved to the cache but not to the vault: %w", err)
	}
	return nil
}

// allInsights reads every insight, reviewed and not, oldest first — the file is
// the whole record, not the pending subset.
func allInsights(db *sql.DB) ([]Insight, error) {
	rows, err := db.Query(
		`SELECT id, kind, text, endpoint_a, endpoint_b, conf, model, created, status
		 FROM dream_insights ORDER BY created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scan(rows)
}

// renderInsights writes the queue the way a person would want to read it, and
// says what they can do about it. The bookkeeping rides in an HTML comment,
// which Obsidian does not render, so the page reads as a plain list and still
// round-trips.
func renderInsights(all []Insight) string {
	pending := 0
	for _, in := range all {
		if in.Status == Pending {
			pending++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: dream-insights\npending: %d\n---\n\n", pending)
	b.WriteString("Connections brain proposed while consolidating. None of these is a\n" +
		"memory yet: nothing here is recalled or packed into context until you\n" +
		"accept it.\n\n")
	b.WriteString("Run `brain dream review` to accept or reject them. Deleting a line here\n" +
		"discards that insight on the next `brain index`; this file is the record,\n" +
		"not the database.\n\n")
	for _, in := range all {
		fmt.Fprintf(&b, "- %s <!-- brain id=%d kind=%s a=%d b=%d conf=%.2f status=%s created=%s",
			field(in.Text), in.ID, in.Kind, in.EndpointA, in.EndpointB,
			in.Conf, in.Status, stamp(in.Created))
		if in.Model != "" {
			fmt.Fprintf(&b, " model=%s", field(in.Model))
		}
		b.WriteString(" -->\n")
	}
	return b.String()
}

func stamp(sec int64) string { return time.Unix(sec, 0).UTC().Format(time.RFC3339) }

// oneLine keeps a record to a single bullet. A newline in the text would split
// one insight into a line the parser reads and a line it silently drops.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// field makes a value safe to sit in the record and safe not to close it early.
// The text here is model output, so it may contain anything — including the
// "-->" that would end the comment and spill bookkeeping onto the page, and the
// "<!--" that would make the parser mistake the user's text for metadata.
//
// Percent-encoding rather than substitution, because the file is the record and
// the cache is rebuilt from it, so the encoding has to be reversible. This is
// internal/secretary's field() with "<" added: that one encodes the values in a
// metadata comment, where "<" is harmless, while the insight's own text sits
// ahead of the comment and can open one.
func field(s string) string {
	var b strings.Builder
	for _, r := range oneLine(s) {
		switch r {
		case '%':
			b.WriteString("%25")
		case '>':
			b.WriteString("%3E")
		case '<':
			b.WriteString("%3C")
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
			case "%3E":
				b.WriteByte('>')
				i += 3
				continue
			case "%3C":
				b.WriteByte('<')
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// looksTruncated reports that the file was cut off rather than edited.
//
// The distinction matters because the two look identical from a count:
// deleting a line is the documented way to discard an insight, and it leaves
// the frontmatter's `pending` stale by exactly the amount a torn write would.
// What a hand edit never does is leave the file ending in the middle of a
// record — so that is what is checked.
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

// parseInsights reads the file back. A line with no bookkeeping comment is
// skipped rather than guessed at: an insight with no id, endpoints or status is
// not something this queue can act on, and inventing them would fabricate the
// citation that makes the queue trustworthy at all.
func parseInsights(raw string) []Insight {
	var out []Insight
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		// LastIndex, not Index: the text is free-form model output that may
		// itself contain "<!--", and brain's own comment is always the last
		// thing on the line. Splitting at the first one truncates the insight
		// at whatever the model happened to quote. See internal/memory's
		// parseKind, where doing it the other way lost user text.
		i := strings.LastIndex(line, "<!--")
		if i < 0 {
			continue
		}
		in := Insight{Text: unfield(strings.TrimSpace(line[2:i])), Status: Pending}
		meta := strings.TrimSuffix(strings.TrimSpace(line[i+len("<!--"):]), "-->")
		for _, f := range strings.Fields(meta) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "id":
				in.ID, _ = strconv.ParseInt(v, 10, 64)
			case "kind":
				in.Kind = Kind(v)
			case "a":
				in.EndpointA, _ = strconv.ParseInt(v, 10, 64)
			case "b":
				in.EndpointB, _ = strconv.ParseInt(v, 10, 64)
			case "conf":
				in.Conf, _ = strconv.ParseFloat(v, 64)
			case "status":
				in.Status = Status(v)
			case "model":
				in.Model = unfield(v)
			case "created":
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					in.Created = t.Unix()
				}
			}
		}
		if in.ID == 0 {
			continue
		}
		if in.Created == 0 {
			in.Created = time.Now().Unix()
		}
		out = append(out, in)
	}
	return out
}

// Import restores dreamed insights from the vault, and is what makes deleting
// the cache survivable for them.
//
// dir is the caller's rather than vaultFor's: `brain index` imports a vault it
// has not bound to a store yet.
//
// Returns how many insights it had to put back — a row already in the cache was
// never lost and is not announced as recovered.
func Import(db *sql.DB, dir string) (int, error) {
	if err := InitQueue(db); err != nil {
		return 0, err
	}
	// The same lock the writers take. The destructive half below reads the file
	// and then removes every row missing from it; run against a file another
	// process is part-way through writing, it would discard insights that are
	// only briefly absent.
	g, err := vault.Lock(dir, lockName)
	if err != nil {
		return 0, err
	}
	defer g.Unlock()

	raw, err := os.ReadFile(InsightsPath(dir))
	if os.IsNotExist(err) {
		// Absent is not empty: a vault written before insights were durable, or
		// a partial restore, says nothing about what is queued. The rows in the
		// cache stand — and are written down now rather than at the next
		// mutation, because nothing mutates a queue nobody is reviewing, so a
		// vault that has insights only in its cache is one rebuild away from
		// losing them, and this is the command people run right before that.
		return 0, flushLocked(db, dir)
	}
	if err != nil {
		return 0, err
	}
	if looksTruncated(string(raw)) {
		return 0, fmt.Errorf(
			"refusing to import an incomplete insight queue (%s ends mid-record); "+
				"restore the file or delete the partial line to accept it as-is", InsightsFile)
	}

	parsed := parseInsights(string(raw))
	keep := map[int64]bool{}
	restored := 0
	for _, in := range parsed {
		// Validate, not trust: the file is editable by hand, and an insight
		// that cannot name the two memories it bridges is the fabrication the
		// queue exists to make impossible. Skipped rather than refused — one
		// mistyped line should not block the rest of the restore — but it is
		// not kept either, so the next flush writes the file without it.
		if err := in.Validate(); err != nil {
			continue
		}
		created, err := upsertInsight(db, in)
		if err != nil {
			return restored, err
		}
		keep[in.ID] = true
		if created {
			restored++
		}
	}

	// A line the user deleted is an insight they no longer want on the record.
	// The row goes with it: a deleted line has no representation left in the
	// vault, and leaving the row would put the text back on the next flush.
	rows, err := db.Query("SELECT id FROM dream_insights")
	if err != nil {
		return restored, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return restored, err
		}
		if !keep[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := db.Exec("DELETE FROM dream_insights WHERE id = ?", id); err != nil {
			return restored, err
		}
	}
	return restored, nil
}

// upsertInsight restores one insight, keeping the id the file carries so
// anything that cited it still resolves. It reports whether the row had to be
// created, which is what "restored N insights" counts.
//
// An existing row is updated rather than left alone, because the file is the
// record: a status edited by hand there is the user's decision, and the point
// of the file saying so is that it takes effect.
func upsertInsight(db *sql.DB, in Insight) (bool, error) {
	var exists int
	if err := db.QueryRow("SELECT COUNT(*) FROM dream_insights WHERE id = ?", in.ID).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		_, err := db.Exec(
			`UPDATE dream_insights SET kind=?, text=?, endpoint_a=?, endpoint_b=?,
			 conf=?, model=?, created=?, status=? WHERE id=?`,
			string(in.Kind), in.Text, in.EndpointA, in.EndpointB,
			in.Conf, in.Model, in.Created, string(in.Status), in.ID)
		return false, err
	}
	_, err := db.Exec(
		`INSERT INTO dream_insights (id, kind, text, endpoint_a, endpoint_b, conf, model, created, status)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		in.ID, string(in.Kind), in.Text, in.EndpointA, in.EndpointB,
		in.Conf, in.Model, in.Created, string(in.Status))
	return err == nil, err
}
