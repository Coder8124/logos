package index

import (
	"os"
	"path/filepath"
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
