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

// The promise every document in this project makes: delete .logos/index.db,
// run `logos index`, lose nothing. Open loops lived only in the cache, so
// `logos loop` went quietly empty after a rebuild — the loop was not closed,
// it was destroyed, and nothing said so.
func TestOpenLoopsSurviveDeletingTheIndex(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	if _, err := Add(db, &Commitment{Text: "call the vendor back", Who: "Priya", DueHint: "friday"}); err != nil {
		t.Fatal(err)
	}

	rebuilt := vaultDB(t, dir) // the cache, thrown away and rebuilt from the vault
	restored, _, err := Import(rebuilt, dir)
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
	if _, _, err := Import(rebuilt, dir); err != nil {
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

	if _, _, err := Import(db, dir); err != nil {
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

	if _, _, err := Import(db, dir); err == nil {
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

	restored, _, err := Import(db, dir)
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
	// cache is one rebuild away from losing them, and `logos index` is the
	// command people run immediately before that.
	if _, err := os.Stat(LoopsPath(dir)); err != nil {
		t.Errorf("importing a vault with no loop file must write one: %v", err)
	}
}

// loops.md is the record and the cache is rebuilt from it, so anything the
// renderer can write the parser has to read back unchanged. It could not:
// field() encoded only a space as "-", while parse turned *every* "-" back into
// a space. A hyphenated name, an ISO due date and a dated source ref — the
// common case, not the edge case — all came back corrupted, and were written
// back corrupted on the next flush.
func TestLoopMetadataSurvivesTheRoundTripThroughTheVault(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	want := &Commitment{
		Text:      "send the tooling PO",
		Who:       "Jean-Luc",
		DueHint:   "2026-09-12",
		SourceRef: "sessions/2026-09-10.md",
	}
	if _, err := Add(db, want); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(LoopsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := parse(string(raw))
	if len(got) != 1 {
		t.Fatalf("parsed %d records from %q, want 1", len(got), raw)
	}
	if got[0].Who != want.Who || got[0].DueHint != want.DueHint || got[0].SourceRef != want.SourceRef {
		t.Errorf("metadata was corrupted by the round trip:\n got who=%q due=%q src=%q\nwant who=%q due=%q src=%q",
			got[0].Who, got[0].DueHint, got[0].SourceRef, want.Who, want.DueHint, want.SourceRef)
	}
}

// The file tells the user a line is theirs to delete, so a line is theirs to
// duplicate — a copy-paste is an expected edit. parse took the *first* "<!--"
// as the start of logos's bookkeeping, so a loop whose own text mentioned one
// was truncated at that point on every import.
func TestALoopWhoseTextContainsAMarkdownCommentIsNotTruncated(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	text := "strip the <!-- hack --> from the page"
	if _, err := Add(db, &Commitment{Text: text}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(LoopsPath(dir))
	got := parse(string(raw))
	if len(got) != 1 || got[0].Text != text {
		t.Errorf("text did not round-trip: %+v, want %q", got, text)
	}
}

// A duplicated line collided with the fingerprint UNIQUE index, which
// ON CONFLICT(id) does not cover. Import returned that error up through
// `logos index` — after some rows had already been upserted and before the
// reconciling delete pass ran, leaving the cache half-imported. A repeated
// commitment is one commitment, not a failed rebuild.
func TestADuplicatedLineInTheLoopFileDoesNotFailTheImport(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	if _, err := Add(db, &Commitment{Text: "book the freight slot"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(LoopsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var dup string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "book the freight slot") {
			dup = strings.Replace(line, "id=1", "id=2", 1)
		}
	}
	if dup == "" {
		t.Fatal("could not find the rendered line to duplicate")
	}
	if err := os.WriteFile(LoopsPath(dir), []byte(string(raw)+dup+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, dir); err != nil {
		t.Fatalf("a duplicated line failed the import: %v", err)
	}
	open, _ := Open_(db)
	if len(open) != 1 {
		t.Errorf("open loops = %+v, want the duplicate collapsed into one", open)
	}
}

// The next write regenerates the whole file from the cache, so a line deleted
// by hand only stayed deleted if `logos index` happened to run first. Without
// that, the next `logos loop add` wrote the struck loop straight back, and the
// user's edit was undone by an unrelated command that said only "tracked".
func TestALoopDeletedByHandStaysDeletedWhenTheNextLoopIsAdded(t *testing.T) {
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

	if _, err := Add(db, &Commitment{Text: "book the freight"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(LoopsPath(dir))
	if strings.Contains(string(after), "renew the parking permit") {
		t.Errorf("the next add wrote a hand-deleted loop back into %s:\n%s", LoopsFile, after)
	}
	if !strings.Contains(string(after), "book the freight") || !strings.Contains(string(after), "send the tooling PO") {
		t.Errorf("%s lost a loop that was never deleted:\n%s", LoopsFile, after)
	}
	open, _ := Open_(db)
	for _, c := range open {
		if c.Text == "renew the parking permit" {
			t.Error("the hand-deleted loop is still open in the cache")
		}
	}
}

// Adopting the file before the write means a loop deleted by hand is gone by the
// time the update runs. Closing it then touched no row and returned nil, so
// `logos loop done 4` printed "done [4]" for a loop the file no longer had.
func TestClosingALoopDeletedByHandSaysItIsGone(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)
	c := Commitment{Text: "renew the parking permit"}
	Add(db, &c)
	Add(db, &Commitment{Text: "send the tooling PO"})
	raw, _ := os.ReadFile(LoopsPath(dir))
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "renew the parking permit") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(LoopsPath(dir), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SetStatus(db, c.ID, Done)
	if err == nil || !strings.Contains(err.Error(), LoopsFile) {
		t.Errorf("closing a loop deleted from %s by hand: err = %v, want one that says the file no longer has it", LoopsFile, err)
	}
}

// strandRoot makes the vault root unwritable, so the next write of loops.md
// fails the way a read-only mount or a full disk does, and returns the function
// that lets writes through again.
func strandRoot(t *testing.T, dir string) (unstrand func()) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			os.Chmod(dir, 0o700)
		}
	})
	return func() {
		restored = true
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func hasLoop(t *testing.T, db *sql.DB, text string) bool {
	t.Helper()
	open, err := Open_(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range open {
		if c.Text == text {
			return true
		}
	}
	return false
}

// The failed write said "saved to the cache but not to the vault", and forgot
// its stamp so the next write would adopt whatever was on disk. That adopt found
// no line for the loop and deleted it as one the user had removed by hand — the
// next `loop add` destroyed the loop it should have written out.
func TestALoopTheVaultRefusedSurvivesTheNextAdd(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	if _, err := Add(db, &Commitment{Text: "call the vendor back"}); err != nil {
		t.Fatal(err)
	}
	unstrand := strandRoot(t, dir)
	if _, err := Add(db, &Commitment{Text: "send Priya the contract"}); err == nil {
		t.Fatal("adding a loop to an unwritable vault reported success")
	}
	unstrand()

	if _, err := Add(db, &Commitment{Text: "book the review room"}); err != nil {
		t.Fatal(err)
	}
	if !hasLoop(t, db, "send Priya the contract") {
		t.Fatal("the next add deleted the loop the vault refused, as though the user had removed its line")
	}
	raw, err := os.ReadFile(LoopsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "send Priya the contract") {
		t.Errorf("the loop is still only in the cache:\n%s", raw)
	}
}

// `logos index` is the full reconcile people run right before deleting the
// cache, so it is where a stranded loop has to be written out — and said to
// be, because the user was told about the failure once, by a process that has
// since exited.
func TestReindexingWritesOutALoopTheVaultNeverGot(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)

	if _, err := Add(db, &Commitment{Text: "call the vendor back"}); err != nil {
		t.Fatal(err)
	}
	unstrand := strandRoot(t, dir)
	if _, err := Add(db, &Commitment{Text: "send Priya the contract"}); err == nil {
		t.Fatal("adding a loop to an unwritable vault reported success")
	}
	unstrand()

	restored, rescued, err := Import(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored != 0 || rescued != 1 {
		t.Errorf("restored %d, rescued %d; want 0 and 1 — a rescue reported as a restore, or not at all, hides that the cache was its only copy", restored, rescued)
	}
	raw, err := os.ReadFile(LoopsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "send Priya the contract") {
		t.Errorf("reindexing left the loop only in the cache:\n%s", raw)
	}
	if _, rescued, _ := Import(db, dir); rescued != 0 {
		t.Errorf("a second reindex rescued %d loops again; the mark outlived the write that settled it", rescued)
	}
}

// The mark is the only thing that stops the next write deleting a loop the vault
// refused, so a mark that did not land is a second failure the caller must hear
// about — not a bookkeeping detail dropped on the error path.
func TestALoopThatCouldNotBeMarkedSaysSo(t *testing.T) {
	dir := t.TempDir()
	db := vaultDB(t, dir)
	if _, err := Add(db, &Commitment{Text: "send Dana the deck"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_mark BEFORE UPDATE OF unflushed ON commitments
		WHEN NEW.unflushed = 1 BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
		t.Fatal(err)
	}
	strandRoot(t, dir)

	_, err := Add(db, &Commitment{Text: "call the vendor back"})
	if err == nil {
		t.Fatal("adding a loop to an unwritable vault reported success")
	}
	if !strings.Contains(err.Error(), "could not mark") {
		t.Errorf("err = %v, want one that says the loop is unprotected from the next write", err)
	}
}
