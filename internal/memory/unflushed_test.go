package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// strand makes the memories directory unwritable so the next export fails the
// way a read-only mount, a full disk or a permissions change does, and returns
// the function that lets writes through again.
func strand(t *testing.T, dir string) (unstrand func()) {
	t.Helper()
	memDir := filepath.Join(dir, Dir)
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(memDir, 0o500); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			os.Chmod(memDir, 0o700)
		}
	})
	return func() {
		restored = true
		if err := os.Chmod(memDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// remembers reports whether the cache still holds a memory with this text.
func remembers(t *testing.T, db *sql.DB, text string) bool {
	t.Helper()
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Text == text {
			return true
		}
	}
	return false
}

// A memory that reached the cache and not the vault used to be indistinguishable
// from a durable one the moment the failing call returned. The caller was told
// the truth once and then exited; recall, doctor and the next index pass all saw
// an ordinary row. The point of the column is that the split outlives that call.
func TestAMemoryTheVaultRefusedIsRecordedAsNotYetDurable(t *testing.T) {
	db, dir := vaultDB(t)
	strand(t, dir)

	m := Memory{Text: "a fact that cannot land", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &m); err == nil {
		t.Fatal("storing into an unwritable vault reported success")
	}

	n, err := Unflushed(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d memories recorded as not yet in the vault, want 1 — nothing after this call can tell one apart from a durable memory", n)
	}
	if !UnflushedIDs(db)[m.ID] {
		t.Errorf("memory %d is unmarked, so recall renders it as settled truth", m.ID)
	}
}

// The command every document here calls safe — delete the index, reindex, lose
// nothing — was the command that destroyed this memory, reaping it as a line the
// user had deleted by hand and reporting the loss as `-0`.
func TestReindexingWritesOutAMemoryTheVaultNeverGotRatherThanDeletingIt(t *testing.T) {
	db, dir := vaultDB(t)

	// One memory that lands first, so fact.md exists and is authoritative —
	// which is the condition under which the second one looks like a deletion.
	landed := Memory{Text: "the alpha widget needs a warm cache", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &landed); err != nil {
		t.Fatal(err)
	}

	unstrand := strand(t, dir)
	stranded := Memory{Text: "a fact that cannot land", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &stranded); err == nil {
		t.Fatal("storing into an unwritable vault reported success")
	}
	unstrand()

	_, rescued, err := Import(db, nil, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if rescued != 1 {
		t.Errorf("reindex rescued %d memories, want 1 — an unannounced rescue is as bad as an unannounced deletion", rescued)
	}
	if !remembers(t, db, stranded.Text) {
		t.Fatal("reindexing deleted the memory it was supposed to protect")
	}

	// And it is in the file now, so the next rebuild needs no rescue at all.
	raw, err := os.ReadFile(filepath.Join(dir, Dir, string(Fact)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), stranded.Text) {
		t.Errorf("the memory is still only in the cache:\n%s", raw)
	}
	if n, _ := Unflushed(db); n != 0 {
		t.Errorf("%d memories still marked as not durable after being written out", n)
	}
}

// A line the user genuinely deleted from the file must still be forgotten.
// Rescuing everything the file does not mention would make the markdown
// read-only in practice, and every memories/<kind>.md invites the opposite.
func TestALineTheUserDeletedByHandIsStillForgotten(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "the staging port is 9090", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, Dir, string(Fact)+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, m.Text) {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if remembers(t, db, m.Text) {
		t.Error("a memory the user deleted from the file came back")
	}
}

// A vault still unwritable at reindex time must keep both the memory and the
// mark. Clearing the mark on a rescue that failed would leave an ordinary-
// looking row that the run after this one deletes.
func TestAVaultThatIsStillUnwritableKeepsTheMemoryAndTheMark(t *testing.T) {
	db, dir := vaultDB(t)

	landed := Memory{Text: "the alpha widget needs a warm cache", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &landed); err != nil {
		t.Fatal(err)
	}
	strand(t, dir)

	stranded := Memory{Text: "a fact that cannot land", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &stranded); err == nil {
		t.Fatal("storing into an unwritable vault reported success")
	}

	if _, _, err := Import(db, nil, "", dir); err == nil {
		t.Error("a rescue that could not write reported success")
	}
	if !remembers(t, db, stranded.Text) {
		t.Error("the memory was dropped while the vault was unwritable")
	}
	if n, _ := Unflushed(db); n != 1 {
		t.Errorf("%d memories marked, want 1", n)
	}
}

// Any later write of the same kind rewrites the whole file and carries the
// stranded memory out with it. That is what keeps the mark from accumulating —
// and a mark left behind would have doctor reporting a problem that has already
// repaired itself.
func TestALaterWriteOfTheSameKindClearsTheMark(t *testing.T) {
	db, dir := vaultDB(t)

	unstrand := strand(t, dir)
	stranded := Memory{Text: "a fact that cannot land", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &stranded); err == nil {
		t.Fatal("storing into an unwritable vault reported success")
	}
	unstrand()

	later := Memory{Text: "the alpha widget needs a warm cache", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &later); err != nil {
		t.Fatal(err)
	}
	if n, _ := Unflushed(db); n != 0 {
		t.Errorf("%d memories still marked after a successful write of the same kind", n)
	}
	raw, err := os.ReadFile(filepath.Join(dir, Dir, string(Fact)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), stranded.Text) {
		t.Errorf("the mark was cleared but the memory is not in the file:\n%s", raw)
	}
}
