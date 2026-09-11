package secretary

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// vaultDB is a store bound to a vault, the way index.Open binds one.
func vaultDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, ""); db.Close() })
	return db
}

// The promise every document in this project makes: delete .brain/index.db,
// run `brain index`, lose nothing. Open loops lived only in the cache, so
// `brain loop` went quietly empty after a rebuild — the loop was not closed,
// it was destroyed, and nothing said so.
func TestOpenLoopsSurviveDeletingTheIndex(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	if _, err := Add(db, &Commitment{Text: "call the vendor back", Who: "Priya", DueHint: "friday"}); err != nil {
		t.Fatal(err)
	}

	rebuilt := vaultDB(t, dir) // the cache, thrown away and rebuilt from the vault
	restored, err := Import(rebuilt, dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 1 {
		t.Fatalf("restored %d loops, want 1", restored)
	}
	open, err := Open_(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("open loops after the rebuild = %v, want the one that was tracked", open)
	}
	if open[0].Text != "call the vendor back" || open[0].Who != "Priya" || open[0].DueHint != "friday" {
		t.Errorf("the loop came back missing what was recorded with it: %+v", open[0])
	}
}

// A loop closed before the rebuild must not come back open. Resurrecting a
// finished commitment is worse than losing it: the user is told to do
// something they already did.
func TestAClosedLoopDoesNotComeBackOpen(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	c := &Commitment{Text: "email Sarah the deck"}
	if _, err := Add(db, c); err != nil {
		t.Fatal(err)
	}
	if err := SetStatus(db, c.ID, Done); err != nil {
		t.Fatal(err)
	}

	rebuilt := vaultDB(t, dir)
	if _, err := Import(rebuilt, dir); err != nil {
		t.Fatal(err)
	}
	if n, _ := OpenCount(rebuilt); n != 0 {
		t.Errorf("open count after the rebuild = %d, want 0", n)
	}
	// And the closure itself survives, so the weekly review can still report it.
	resolved, err := ResolvedSince(rebuilt, 0, 1<<62)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Text != "email Sarah the deck" {
		t.Errorf("ResolvedSince after the rebuild = %+v, want the closed loop", resolved)
	}
}

// The file is the record. A line struck out by hand in an editor is how a
// person closes a loop without the CLI, and the next index has to honour it.
func TestALoopDeletedFromTheFileIsGoneAfterAnImport(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	Add(db, &Commitment{Text: "renew the parking permit"})
	Add(db, &Commitment{Text: "send the tooling PO"})

	raw, err := os.ReadFile(LoopsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "renew the parking permit") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(LoopsPath(dir), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, dir); err != nil {
		t.Fatal(err)
	}
	open, _ := Open_(db)
	if len(open) != 1 || open[0].Text != "send the tooling PO" {
		t.Errorf("open loops = %+v, want only the loop that was left in the file", open)
	}
}

// A file another process is part-way through writing is not a list of
// deletions. Acting on it would drop every loop the missing tail held.
func TestImportRefusesATruncatedLoopFile(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)
	Add(db, &Commitment{Text: "book the freight slot"})

	raw, _ := os.ReadFile(LoopsPath(dir))
	torn := string(raw[:len(raw)-20]) // ends mid-record
	if err := os.WriteFile(LoopsPath(dir), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, dir); err == nil {
		t.Fatal("expected an incomplete loop file to be refused, got no error")
	}
	if n, _ := OpenCount(db); n != 1 {
		t.Errorf("a refused import must change nothing; open count = %d, want 1", n)
	}
}

// No file at all is a vault written before loops were durable, or a partial
// restore. It says nothing about what is open, so it must not reap anything.
func TestAnAbsentLoopFileLeavesTheCacheAlone(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)
	Add(db, &Commitment{Text: "chase the invoice"})
	if err := os.Remove(LoopsPath(dir)); err != nil {
		t.Fatal(err)
	}

	restored, err := Import(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 0 {
		t.Errorf("restored %d from an absent file, want 0", restored)
	}
	if n, _ := OpenCount(db); n != 1 {
		t.Errorf("open count = %d, want the cached loop left alone", n)
	}
	// And it is written down on the way past: a vault holding loops only in its
	// cache is one rebuild away from losing them, and `brain index` is the
	// command people run immediately before that.
	if _, err := os.Stat(LoopsPath(dir)); err != nil {
		t.Errorf("importing a vault with no loop file must write one: %v", err)
	}
}
