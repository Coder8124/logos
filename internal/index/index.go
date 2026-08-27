// Package index is the rebuildable cache.
//
// Nothing here is authoritative. Delete .brain/index.db, run `brain index`, and
// you are back to identical state derived entirely from the markdown.
package index

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/vault"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS notes (
    slug       TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    body       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    first_seen INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS aliases (
    slug  TEXT NOT NULL REFERENCES notes(slug) ON DELETE CASCADE,
    alias TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS edges (
    src_slug TEXT NOT NULL REFERENCES notes(slug) ON DELETE CASCADE,
    pred     TEXT NOT NULL,
    obj      TEXT NOT NULL,
    conf     REAL NOT NULL,
    src      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS edges_src ON edges(src_slug);
CREATE INDEX IF NOT EXISTS edges_obj ON edges(obj);
CREATE TABLE IF NOT EXISTS embeddings (
    slug TEXT PRIMARY KEY REFERENCES notes(slug) ON DELETE CASCADE,
    dim  INTEGER NOT NULL,
    vec  BLOB NOT NULL
);
`

type Index struct {
	Vault string
	DB    *sql.DB
}

type SyncReport struct {
	Added, Updated, Removed, Unchanged int
}

func Open(vaultDir string) (*Index, error) {
	dir := filepath.Join(vaultDir, ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		return nil, err
	}
	// modernc's driver is not safe for unlimited concurrent writers; the whole
	// application is single-writer anyway.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if _, err := db.Exec(ftsSchema); err != nil {
		return nil, err
	}
	migrate(db)
	// Pair the memory store with the vault it belongs to. This is the one place
	// that holds both, and doing it here means every write path gets a durable
	// copy without each caller having to remember to ask for one.
	memory.SetVault(vaultDir)
	return &Index{Vault: vaultDir, DB: db}, nil
}

// SyncMemories reconciles the memory store against its markdown in the vault.
//
// The mirror of Sync, for the other half of what the system knows. Notes are
// rebuilt from files on every index; memories were not, which made "delete the
// database and reindex" true for one half and quietly destructive for the
// other. Running both from one command is what makes the guarantee whole.
//
// Returns how many memories the vault held.
func (ix *Index) SyncMemories(p *provider.Provider, embedModel string) (int, error) {
	return memory.Import(ix.DB, p, embedModel, ix.Vault)
}

// migrate adds columns to databases created before they existed. ALTER TABLE
// ADD COLUMN is idempotent-safe here because we swallow the "duplicate column"
// error — cheaper and clearer than querying the schema first.
func migrate(db *sql.DB) {
	db.Exec("ALTER TABLE notes ADD COLUMN first_seen INTEGER NOT NULL DEFAULT 0")

	// FTS backfill: notes indexed before the FTS table existed never populated
	// it, and Sync only refreshes FTS for notes whose content changed — so a
	// vault whose notes are all unchanged would have empty lexical search.
	// Rebuild it once when it is out of step with the notes table.
	var notesN, ftsN int
	db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&notesN)
	db.QueryRow("SELECT COUNT(*) FROM notes_fts").Scan(&ftsN)
	if notesN > 0 && ftsN < notesN {
		db.Exec("DELETE FROM notes_fts")
		db.Exec(`INSERT INTO notes_fts (slug, title, body) SELECT slug, title, body FROM notes`)
	}
}

func (ix *Index) Close() error { return ix.DB.Close() }

// Sync reads every note in the vault and reconciles it against the cache. Only
// notes whose content hash changed are re-parsed and marked for re-embedding.
func (ix *Index) Sync() (SyncReport, error) {
	var rep SyncReport

	var files []string
	err := filepath.WalkDir(ix.Vault, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries must not abort the whole walk
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			// memories/ is the memory store written down, not a set of notes.
			// Indexing it would put every remembered fact into vault search a
			// second time, so the same preference comes back as a memory and as
			// a note that happens to quote it. It is reconciled below instead.
			if d.Name() == memory.Dir && filepath.Dir(path) == ix.Vault {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return rep, err
	}

	tx, err := ix.DB.Begin()
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()

	seen := make(map[string]bool, len(files))

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return rep, fmt.Errorf("reading %s: %w", path, err)
		}
		n := vault.Parse(ix.Vault, path, string(raw))
		seen[n.Slug] = true

		var existing string
		err = tx.QueryRow("SELECT hash FROM notes WHERE slug = ?", n.Slug).Scan(&existing)
		switch {
		case err == sql.ErrNoRows:
			rep.Added++
		case err != nil:
			return rep, err
		case existing == n.Hash:
			rep.Unchanged++
			continue
		default:
			rep.Updated++
		}

		if _, err := tx.Exec(
			`INSERT INTO notes (slug, path, title, kind, body, hash, first_seen) VALUES (?,?,?,?,?,?,?)
			 ON CONFLICT(slug) DO UPDATE SET path=excluded.path, title=excluded.title,
			   kind=excluded.kind, body=excluded.body, hash=excluded.hash, first_seen=excluded.first_seen`,
			n.Slug, n.Path, n.Title, n.Kind, n.Body, n.Hash, n.FirstSeen,
		); err != nil {
			return rep, err
		}

		// Content changed, so every derived row is stale by definition.
		for _, q := range []string{
			"DELETE FROM edges WHERE src_slug = ?",
			"DELETE FROM aliases WHERE slug = ?",
			"DELETE FROM embeddings WHERE slug = ?",
		} {
			if _, err := tx.Exec(q, n.Slug); err != nil {
				return rep, err
			}
		}

		// Keep the FTS row in step with the note inside the same transaction, so
		// lexical and semantic search never see different vaults.
		if _, err := tx.Exec("DELETE FROM notes_fts WHERE slug = ?", n.Slug); err != nil {
			return rep, err
		}
		if _, err := tx.Exec("INSERT INTO notes_fts (slug, title, body) VALUES (?,?,?)",
			n.Slug, n.Title, n.Body); err != nil {
			return rep, err
		}

		for _, a := range n.Aliases {
			if _, err := tx.Exec("INSERT INTO aliases (slug, alias) VALUES (?,?)", n.Slug, a); err != nil {
				return rep, err
			}
		}
		for _, e := range n.Edges {
			if _, err := tx.Exec(
				"INSERT INTO edges (src_slug, pred, obj, conf, src) VALUES (?,?,?,?,?)",
				n.Slug, e.Pred, e.Obj, e.Conf, string(e.Src),
			); err != nil {
				return rep, err
			}
		}
	}

	// Notes deleted from disk must leave the cache too, or retrieval starts
	// citing files that no longer exist.
	rows, err := tx.Query("SELECT slug FROM notes")
	if err != nil {
		return rep, err
	}
	var stale []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return rep, err
		}
		if !seen[slug] {
			stale = append(stale, slug)
		}
	}
	rows.Close()

	for _, slug := range stale {
		if _, err := tx.Exec("DELETE FROM notes WHERE slug = ?", slug); err != nil {
			return rep, err
		}
		// FTS is not wired to the notes table by a foreign key (it is a virtual
		// table), so it must be cleaned up explicitly.
		if _, err := tx.Exec("DELETE FROM notes_fts WHERE slug = ?", slug); err != nil {
			return rep, err
		}
		rep.Removed++
	}

	return rep, tx.Commit()
}

// EmbedPending embeds every note that lacks a vector, in batches — a round trip
// per note makes a 2000-note vault take minutes instead of seconds.
func (ix *Index) EmbedPending(p *provider.Provider, model string, batch int) (int, error) {
	if batch < 1 {
		batch = 1
	}

	rows, err := ix.DB.Query(`
		SELECT n.slug, n.title, n.kind, n.body FROM notes n
		LEFT JOIN embeddings e ON e.slug = n.slug WHERE e.slug IS NULL`)
	if err != nil {
		return 0, err
	}

	type pending struct{ slug, text string }
	var todo []pending
	for rows.Next() {
		var slug, title, kind, body string
		if err := rows.Scan(&slug, &title, &kind, &body); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, pending{slug, fmt.Sprintf("%s (%s)\n\n%s", title, kind, body)})
	}
	rows.Close()

	done := 0
	for start := 0; start < len(todo); start += batch {
		chunk := todo[start:min(start+batch, len(todo))]

		inputs := make([]string, len(chunk))
		for i, t := range chunk {
			inputs[i] = t.text
		}

		vectors, err := p.Embed(model, inputs)
		if err != nil {
			return done, err
		}
		for i, vec := range vectors {
			if _, err := ix.DB.Exec(
				`INSERT INTO embeddings (slug, dim, vec) VALUES (?,?,?)
				 ON CONFLICT(slug) DO UPDATE SET dim=excluded.dim, vec=excluded.vec`,
				chunk[i].slug, len(vec), floatsToBlob(vec),
			); err != nil {
				return done, err
			}
			done++
		}
	}
	return done, nil
}

func (ix *Index) count(table string) (int, error) {
	var n int
	err := ix.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	return n, err
}

func (ix *Index) NoteCount() (int, error) { return ix.count("notes") }
func (ix *Index) EdgeCount() (int, error) { return ix.count("edges") }

func floatsToBlob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func blobToFloats(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}
