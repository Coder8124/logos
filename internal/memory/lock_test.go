package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The stamp is a claim: "the bytes on disk are the ones we wrote". Forgetting
// the last memory of a kind deletes the file, so the read-back that renews the
// claim fails — and the claim about a file that no longer exists was left
// standing. Restore that file from a backup, a sync client or a git checkout
// and the stale claim is wrong in the one direction that loses data: Reconcile
// recognises the restored bytes as its own, skips the pass that would have
// adopted them, and the next write regenerates the file without them.
//
// Fails on the old flush, which only stamped on a successful read-back.
func TestAMemoryRestoredIntoAFileWeOnceDeletedIsNotSilentlyDropped(t *testing.T) {
	db, dir := store(t)
	path := filepath.Join(dir, Dir, string(Fact)+".md")

	if _, err := Store(db, nil, "", &Memory{Text: "the deploy key lives in 1Password", Kind: Fact, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Forget it, which removes the file — the moment the stamp went stale.
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected one memory before forgetting, got %d", len(all))
	}
	if err := Forget(db, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone after forgetting the only memory of its kind", path)
	}

	// The restore: exactly the bytes that were there before, as a backup or a
	// sync client would put them back.
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}

	// Any later write reconciles first. It must see the restored file as
	// something to adopt, not as a file it wrote itself.
	if _, err := Store(db, nil, "", &Memory{Text: "staging runs on the same key", Kind: Fact, Source: "test"}); err != nil {
		t.Fatal(err)
	}

	got, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Text)
	}
	if len(got) != 2 {
		t.Fatalf("after restoring a file we had deleted, memories = %v; want the restored one adopted alongside the new one", texts)
	}
}

// Two proposals arriving at once used to race on memories/pending.md: both read
// the queue, both rewrite the whole file, and the later write won with a
// snapshot taken before the other proposal existed. Nothing reported a problem
// — the loss only surfaced on the next `brain index`, where ImportPending reads
// a queued id missing from the file as a line the user deleted and rejects it.
//
// A proposal the user never saw, discarded on their behalf.
//
// A guard, not a regression test, and worth being exact about which: the window
// is narrow enough that this passes on the unfixed code as often as not, the
// same way TestConcurrentRemembersAllSurvive did until CI caught it at round 17
// of 20. It is here to fail eventually and loudly on a machine that is not this
// one; the argument that the window is real is in flushPendingLocked.
func TestConcurrentProposalsAllReachTheQueueFile(t *testing.T) {
	// Opened the way index.Open opens the real thing — busy_timeout, one
	// connection — so writers queue on SQLite instead of failing with
	// SQLITE_BUSY. Without that, concurrent writers fall over on the database
	// before they ever reach the file, and this would be testing the wrong
	// layer: the loss being asserted here happens in memories/pending.md, after
	// every insert has succeeded.
	db, dir := busyTolerantStore(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Store(db, nil, "", &Memory{
				Text:        proposalText(i),
				Kind:        Fact,
				Source:      "test",
				Quarantined: true,
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("proposal %d: %v", i, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, Dir, PendingFile))
	if err != nil {
		t.Fatalf("reading the queue file: %v", err)
	}

	// The file is the only record that survives deleting the cache, so it — not
	// the database — is what has to hold all eight.
	missing := 0
	for i := 0; i < n; i++ {
		if !strings.Contains(string(raw), proposalText(i)) {
			missing++
			t.Errorf("%q reached the cache but not memories/%s", proposalText(i), PendingFile)
		}
	}
	if missing > 0 {
		t.Fatalf("%d of %d proposals would be rejected by the next `brain index` without the user ever seeing them", missing, n)
	}
}

// busyTolerantStore is `store` with the DSN and connection limit index.Open
// uses, for tests that write concurrently.
func busyTolerantStore(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })
	return db, dir
}

func proposalText(i int) string {
	return "proposal number " + string(rune('a'+i)) + " that nobody has reviewed"
}
