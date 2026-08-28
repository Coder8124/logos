package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/vault"
)

// Memories, written down.
//
// The project's second principle says the database is a cache: delete
// .brain/index.db, reindex, get identical state. That was true of notes and
// checkpoints and quietly false of memories, which lived only in the cache. A
// benchmark case caught it, and it is worth being precise about how bad it was:
// `rm -rf .brain` — the thing the README suggests you do — destroyed every
// preference, every fact about a person, every piece of standing context, with
// no warning and nothing to restore from.
//
// So memories are files now. One per kind, because a memory is one line and
// giving each its own note would bury a vault under thousands of one-line files
// that nobody wants to browse. Grouping by kind also produces the page a person
// actually wants: open memories/preference.md and read what it thinks you like.
//
// The bookkeeping — id, confidence, salience, provenance — rides in an HTML
// comment. Obsidian does not render those, so the file reads as a clean bullet
// list while still round-tripping everything the store needs. Edit the prose,
// delete a line to forget it, reindex; the file is the record.

// Dir is where memories live inside the vault.
const Dir = "memories"

// The vault a store writes through to, keyed by the database it belongs to.
//
// This used to be one package-level string, on the reasoning that threading a
// path through Store, Forget, Learn and both dream passes was the worse cost.
// That was true right up until a process could hold two vaults at once, which
// the public API made ordinary: the second Open overwrote the binding, so one
// vault's memories were written into the other's directory and then erased by
// its next whole-file flush. Neither vault ended up with the memory.
//
// Keying by *sql.DB restores the pairing without touching a single call
// signature — the database handle is already threaded everywhere the path would
// have had to go.
var (
	vaultMu sync.RWMutex
	vaults  = map[*sql.DB]string{}
)

// SetVault tells one store where its durable copy belongs. An empty dir unbinds
// it, which disables writing through — what tests that only care about the
// cache want, and what index.Close does so a closed handle leaves nothing
// behind.
func SetVault(db *sql.DB, dir string) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	if dir == "" {
		delete(vaults, db)
		return
	}
	vaults[db] = dir
}

// vaultFor returns the vault bound to a database, or "" if it has none.
func vaultFor(db *sql.DB) string {
	vaultMu.RLock()
	defer vaultMu.RUnlock()
	return vaults[db]
}

// kinds is the fixed set, so exporting is deterministic and a kind with no
// memories still gets its file emptied rather than left stale.
var kinds = []Kind{Preference, Person, Fact, Context}

// flush writes one kind's file. Called after every mutation.
//
// Whole-file rewrites rather than an append log: a memory can be edited,
// superseded or forgotten, and reconstructing current state from a log on every
// read is how the cache became authoritative in the first place. The files are
// small — a few hundred short lines each — so this is cheaper than the SQLite
// write that precedes it.
//
// The error is returned rather than dropped. It used to be discarded, which
// meant a read-only directory or a full disk produced a memory that existed
// only in the cache while the caller was told it was saved — and the cache is
// the thing the README tells people to delete. A write that cannot be made
// durable has to say so.
func flush(db *sql.DB, kind Kind) error {
	dir := vaultFor(db)
	if dir == "" {
		return nil
	}
	if err := ExportKind(db, dir, kind); err != nil {
		return fmt.Errorf("memory saved to the cache but not to the vault: %w", err)
	}
	return nil
}

// Export writes every kind to the vault.
func Export(db *sql.DB, dir string) error {
	for _, k := range kinds {
		if err := ExportKind(db, dir, k); err != nil {
			return err
		}
	}
	return nil
}

// ExportKind writes one kind's memories to memories/<kind>.md.
//
// Superseded memories are left out. They are history, not knowledge — the
// memory_log already records that they existed and what replaced them, and
// carrying them here would put values the user has already corrected back into
// a file the user reads.
func ExportKind(db *sql.DB, dir string, kind Kind) error {
	rows, err := db.Query(
		`SELECT id, text, salience, confidence, project, source, created, uses
		 FROM memories WHERE kind = ? AND superseded = 0 ORDER BY created, id`, string(kind))
	if err != nil {
		return err
	}
	defer rows.Close()

	var mems []Memory
	for rows.Next() {
		var m Memory
		var project, source sql.NullString
		if err := rows.Scan(&m.ID, &m.Text, &m.Salience, &m.Confidence,
			&project, &source, &m.Created, &m.Uses); err != nil {
			return err
		}
		m.Kind, m.Project, m.Source = kind, project.String, source.String
		mems = append(mems, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	path := filepath.Join(dir, Dir, string(kind)+".md")
	if len(mems) == 0 {
		// Remove rather than leave an empty file: a kind nobody uses should not
		// clutter the vault, and its absence is not ambiguous.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return vault.WriteAtomic(path, []byte(renderKind(kind, mems)))
}

func renderKind(kind Kind, mems []Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: memory-store\nkind: %s\ncount: %d\n---\n\n", kind, len(mems))
	fmt.Fprintf(&b, "%s\n\n", blurb(kind))
	b.WriteString("Edit any line to correct it. Delete a line to forget it. " +
		"Then run `brain index` — this file is the record, not the database.\n\n")
	for _, m := range mems {
		fmt.Fprintf(&b, "- %s <!-- brain id=%d conf=%.2f sal=%.2f src=%s created=%s uses=%d",
			oneLine(m.Text), m.ID, m.Confidence, m.Salience, orDash(m.Source),
			time.Unix(m.Created, 0).UTC().Format(time.RFC3339), m.Uses)
		if m.Project != "" {
			fmt.Fprintf(&b, " project=%s", m.Project)
		}
		b.WriteString(" -->\n")
	}
	return b.String()
}

func blurb(kind Kind) string {
	switch kind {
	case Preference:
		return "How you like things done."
	case Person:
		return "People, and what matters about them."
	case Context:
		return "Standing context — what is going on around you."
	default:
		return "Durable facts about you and your work."
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return strings.ReplaceAll(s, " ", "-")
}

// oneLine keeps a memory on one line. Memories are single statements; a stray
// newline would silently split one into two on the next import.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// Import rebuilds the store from the vault. This is what makes `rm -rf .brain`
// survivable.
//
// The files win. Where a memory exists in both places the text, confidence and
// provenance are taken from markdown, because the whole point is that a person
// can open the file, correct a fact, and have the correction stick. Memories
// present in the cache but absent from the file are deleted — that is how
// deleting a line forgets something.
//
// Vectors are not stored in the file; anything without one is re-embedded here,
// which is the same work `brain index` already does for notes.
func Import(db *sql.DB, p *provider.Provider, embedModel, dir string) (int, error) {
	if err := Init(db); err != nil {
		return 0, err
	}

	var imported int
	keep := map[int64]bool{}

	// Only kinds whose file was read and found intact are authoritative. A kind
	// whose file is missing or truncated says nothing about what the user wants
	// forgotten, so its cached memories are left alone below.
	authoritative := map[Kind]bool{}
	var damaged []string

	for _, kind := range kinds {
		raw, err := os.ReadFile(filepath.Join(dir, Dir, string(kind)+".md"))
		if os.IsNotExist(err) {
			// Absence of information, not an instruction to delete. A sync client
			// mid-restore, a partial rsync, or a crash between two flushes all
			// produce exactly this, and reaping here forgot the whole kind.
			continue
		}
		if err != nil {
			return imported, err
		}

		parsed := parseKind(kind, string(raw))

		// A file that ends mid-line was interrupted rather than edited. Acting on
		// it would delete every memory the missing tail held.
		if looksTruncated(string(raw)) {
			damaged = append(damaged, string(kind)+".md ends mid-record")
			continue
		}

		for _, m := range parsed {
			id, err := upsert(db, p, embedModel, m)
			if err != nil {
				return imported, err
			}
			keep[id] = true
			imported++
		}
		authoritative[kind] = true
	}

	// A damaged file is reported and never acted on. Importing what survived
	// would be the same silent deletion this guard exists to prevent, and the
	// user needs to know before they overwrite the file by writing a new memory.
	if len(damaged) > 0 {
		return imported, fmt.Errorf(
			"refusing to import an incomplete memory store (%s); "+
				"restore the file or delete the count line to accept it as-is",
			strings.Join(damaged, "; "))
	}

	// Nothing on disk means nothing was ever exported — a store predating this,
	// or a vault that has never held memories. Deleting the cache in that case
	// would destroy the very data this function exists to protect, so it exports
	// instead of importing.
	if len(authoritative) == 0 {
		return 0, Export(db, dir)
	}

	rows, err := db.Query("SELECT id, kind FROM memories WHERE superseded = 0")
	if err != nil {
		return imported, err
	}
	var orphans []int64
	for rows.Next() {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			rows.Close()
			return imported, err
		}
		if !keep[id] && authoritative[Kind(kind)] {
			orphans = append(orphans, id)
		}
	}
	rows.Close()
	for _, id := range orphans {
		Forget(db, id)
	}
	return imported, nil
}

// upsert writes one parsed memory back, preserving its id so that references in
// memory_log, and anything the user has cited, keep pointing at the same fact.
func upsert(db *sql.DB, p *provider.Provider, embedModel string, m Memory) (int64, error) {
	var vec []byte
	if p != nil {
		if vecs, err := p.Embed(embedModel, []string{m.Text}); err == nil && len(vecs) == 1 {
			vec = floatsToBlob(vecs[0])
		}
	}

	if m.ID > 0 {
		res, err := db.Exec(
			`UPDATE memories SET text=?, kind=?, salience=?, confidence=?, project=?,
			 source=?, created=?, uses=?, fingerprint=?, vec=COALESCE(?, vec)
			 WHERE id = ?`,
			m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source,
			m.Created, m.Uses, fingerprint(m.Text), vec, m.ID)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return m.ID, nil
		}
		// The row is gone — the cache was wiped. Restore it under its old id.
		_, err = db.Exec(
			`INSERT INTO memories (id, text, kind, salience, confidence, project, source, created, uses, vec, fingerprint)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			m.ID, m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project,
			m.Source, m.Created, m.Uses, vec, fingerprint(m.Text))
		if err == nil {
			logEvent(db, m.ID, EvCreated, m.Text, 0)
		}
		return m.ID, err
	}

	// No id in the comment: a line somebody typed by hand. Give it one.
	res, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, project, source, created, uses, vec, fingerprint)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.Text, string(m.Kind), m.Salience, m.Confidence, m.Project, m.Source,
		m.Created, m.Uses, vec, fingerprint(m.Text))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	logEvent(db, id, EvCreated, m.Text, 0)
	return id, nil
}

// parseKind reads back what renderKind wrote.
//
// Tolerant on purpose: a hand-written line with no metadata comment is a valid
// memory. Telling someone their file is truth and then rejecting what they type
// into it would be a lie.
func parseKind(kind Kind, raw string) []Memory {
	var out []Memory
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		body := strings.TrimSpace(line[2:])

		m := Memory{Kind: kind, Salience: 0.5, Confidence: 0.7, Created: time.Now().Unix()}
		if i := strings.Index(body, "<!--"); i >= 0 {
			meta := body[i:]
			body = strings.TrimSpace(body[:i])
			applyMeta(&m, meta)
		}
		if body == "" {
			continue
		}
		m.Text = body
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// looksTruncated reports that a store file was cut off rather than edited.
//
// The distinction is the whole difficulty here, because the two look identical
// from a count: deleting a line is the documented way to forget something, and
// it leaves the frontmatter's count stale by exactly the amount a torn write
// would. Enforcing the count breaks the feature the format exists to provide.
//
// What a hand edit never does is leave the file ending in the middle of a
// record. So that is what is checked: unterminated frontmatter, or a final
// bullet whose metadata comment was opened and never closed. A write
// interrupted at an arbitrary byte lands mid-line almost every time.
//
// A tear that happens to land exactly on a line boundary is indistinguishable
// from a deliberate deletion and is accepted. That is a real gap, and it is the
// narrower one: brain's own writes go through vault.WriteAtomic, which replaces
// the file by rename and cannot tear, so this only arises when something else —
// a sync client, a crashed editor — is writing the file.
func looksTruncated(raw string) bool {
	trimmed := strings.TrimRight(raw, " \t\r\n")
	if trimmed == "" {
		return false
	}
	// Frontmatter opened and never closed.
	if rest, ok := strings.CutPrefix(trimmed, "---"); ok && !strings.Contains(rest, "\n---") {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	// A record whose bookkeeping comment was opened and never closed.
	return strings.HasPrefix(last, "- ") &&
		strings.Contains(last, "<!--") && !strings.Contains(last, "-->")
}

func applyMeta(m *Memory, meta string) {
	meta = strings.TrimSuffix(strings.TrimPrefix(meta, "<!--"), "-->")
	for _, field := range strings.Fields(meta) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			m.ID, _ = strconv.ParseInt(value, 10, 64)
		case "conf":
			m.Confidence, _ = strconv.ParseFloat(value, 64)
		case "sal":
			m.Salience, _ = strconv.ParseFloat(value, 64)
		case "src":
			if value != "-" {
				m.Source = value
			}
		case "uses":
			n, _ := strconv.Atoi(value)
			m.Uses = n
		case "project":
			m.Project = value
		case "created":
			// RFC3339 now; the bare date is what older files carry. Recording
			// only the date collapsed two memories written hours apart onto the
			// same timestamp, and supersession decides by which came later.
			for _, layout := range []string{time.RFC3339, "2006-01-02"} {
				if t, err := time.Parse(layout, value); err == nil {
					m.Created = t.Unix()
					break
				}
			}
		}
	}
}
