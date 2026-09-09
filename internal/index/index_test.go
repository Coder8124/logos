package index

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Several processes opening one vault at the same moment is the normal first
// run: a coding agent's MCP server, the editor's, and a command typed in a
// terminal. Converting a fresh database to WAL takes an exclusive lock that
// busy_timeout does not wait for, so all but one used to get SQLITE_BUSY —
// and Open turned that into a hard failure, which the user read as "the tool
// is broken" rather than "try again".
func TestManyOpensOnAColdVaultAllSucceed(t *testing.T) {
	v := t.TempDir()

	const n = 12
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ix, err := Open(v)
			if err != nil {
				errs <- err
				return
			}
			defer ix.Close()
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent open of a cold vault failed: %v", err)
		}
	}
}

// A vault indexed before 0.3.0 still has the ambient-capture tables on disk —
// they lived only in .brain/index.db, never the vault, so dropping them loses
// nothing the vault-is-truth promise covers. But a silent schema drop is the
// exact failure this codebase keeps re-fixing (memories, working notes,
// checkpoints, the review queue), so the drop must say how many rows it threw
// away, on stdout, with the number.
func TestDeadAmbientTablesAreDroppedAndAnnounced(t *testing.T) {
	v := t.TempDir()
	ix, err := Open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	// Simulate a vault carried over from before the cut: a table that
	// InitStore used to create, holding rows migrate() has never seen.
	if _, err := ix.DB.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, kind TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.DB.Exec(`INSERT INTO events (kind) VALUES ('url'), ('commit')`); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	dropDeadAmbientTables(ix.DB)
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if !strings.Contains(string(out), "dropped 2 row") {
		t.Errorf("drop was not announced with a row count: %q", out)
	}
	var name string
	err = ix.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='events'").Scan(&name)
	if err != sql.ErrNoRows {
		t.Errorf("events table still exists after dropDeadAmbientTables, err=%v", err)
	}

	// A vault that never had the table — every vault created fresh under
	// 0.3.0 — must stay silent. Announcing a drop of nothing is its own bug.
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w2
	dropDeadAmbientTables(ix.DB)
	w2.Close()
	os.Stdout = oldStdout
	out2, _ := io.ReadAll(r2)
	if len(out2) != 0 {
		t.Errorf("second call with nothing left to drop printed %q, want silence", out2)
	}
}

// "Is the index behind?" used to be answered by comparing the newest file's
// mtime against MAX(first_seen) — a date parsed out of frontmatter, so always
// midnight. Every vault touched after midnight therefore reported as hours
// stale the moment after a successful index, which is a health check that
// cries wolf every day and gets ignored by the second week.
func TestSyncRecordsWhenItRan(t *testing.T) {
	v := t.TempDir()
	if err := os.MkdirAll(filepath.Join(v, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: note\ntitle: a note\nfirst_seen: 2020-01-01\n---\n\nsomething\n"
	if err := os.WriteFile(filepath.Join(v, "notes", "a.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	ix, err := Open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	before := time.Now().Unix()
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := ix.DB.QueryRow("SELECT value FROM meta WHERE key = 'last_sync'").Scan(&got); err != nil {
		t.Fatalf("a completed sync recorded no last_sync: %v", err)
	}
	if got < before {
		t.Errorf("last_sync is %d, before the sync that set it (%d)", got, before)
	}
}

// ingest/ is a review queue, not a set of notes. Candidates in it are
// unreviewed distillates of other agents' transcripts — unverified by
// construction, and full of text this vault's owner never wrote. Indexing them
// put all of it into retrieval: on a real machine, ingesting 53 transcripts
// buried `search` under candidate chunks and made `resume batteringram` quote
// 97 chunks harvested from a different project entirely. Pending means
// unreviewed means it does not answer a question yet. Same reasoning as
// memories/ above it, and the queue does not need the index — Pending reads
// the markdown straight off disk.
func TestPendingIngestCandidatesAreNotIndexedIntoRetrieval(t *testing.T) {
	v := t.TempDir()
	if err := os.MkdirAll(filepath.Join(v, "ingest", "widgets"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := "---\ntype: ingest_candidate\nstatus: pending\n---\n\n# harvested\n\nzarquon deployment notes\n"
	if err := os.WriteFile(filepath.Join(v, "ingest", "widgets", "codex-1.md"), []byte(candidate), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "# Real note\n\nzarquon is the staging cluster\n"
	if err := os.WriteFile(filepath.Join(v, "real.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := Open(v)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	var paths []string
	rows, err := ix.DB.Query(`SELECT path FROM notes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	for _, p := range paths {
		if strings.Contains(filepath.ToSlash(p), "/ingest/") {
			t.Errorf("an unreviewed candidate reached the index: %s", p)
		}
	}
	var sawReal bool
	for _, p := range paths {
		if strings.Contains(p, "real") {
			sawReal = true
		}
	}
	if !sawReal {
		t.Errorf("the ordinary note was lost along with the queue: %v", paths)
	}
}
