package mcpserver

import (
	"strings"
	"testing"
)

// An agent reading a recalled memory has no way to tell one the vault holds
// from one only the cache holds — and the difference matters, because the
// second disappears with a file the docs call safe to delete. The memory is
// still true and still worth using; what the label adds is that it should not
// be relied on tomorrow.
func TestRecallSaysWhenAMemoryIsNotYetInTheVault(t *testing.T) {
	db := testDB(t)
	m := seedMemory(t, db, "the staging port is 9090", "kestrel")
	if _, err := db.Exec("UPDATE memories SET unflushed = 1 WHERE id = ?", m.ID); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Server: &Server{DB: db}}

	out, err := sess.recall("staging port", 5, "kestrel", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not yet saved to the vault") {
		t.Errorf("a memory one wipe from gone is recalled as settled truth:\n%s", out)
	}
}

// list_memories renders a line away from recall and had to be told separately;
// this is the one whose ids get handed to forget.
func TestListMemoriesSaysWhenAMemoryIsNotYetInTheVault(t *testing.T) {
	db := testDB(t)
	m := seedMemory(t, db, "the staging port is 9090", "kestrel")
	if _, err := db.Exec("UPDATE memories SET unflushed = 1 WHERE id = ?", m.ID); err != nil {
		t.Fatal(err)
	}
	srv := &Server{DB: db}

	out, err := srv.listMemories("kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not yet saved to the vault") {
		t.Errorf("the list gives no sign the memory is only in the cache:\n%s", out)
	}
}

// And a durable memory carries no label at all — a note on every line is a
// note on none.
func TestADurableMemoryCarriesNoDurabilityLabel(t *testing.T) {
	db := testDB(t)
	seedMemory(t, db, "the staging port is 9090", "kestrel")
	srv := &Server{DB: db}

	out, err := srv.listMemories("kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not yet saved to the vault") {
		t.Errorf("a memory that is in the vault is labelled as though it were not:\n%s", out)
	}
}
