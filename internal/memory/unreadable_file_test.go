package memory

import (
	"bytes"
	"database/sql"
	"os"
	"runtime"
	"testing"
)

// storeUnreadable stores one fact and makes its own file unreadable, the
// state a chmod, a sync client mid-download or a disk error leaves behind.
func storeUnreadable(t *testing.T) (db *sql.DB, dir, path string, id int64) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a file its owner cannot read")
	}
	db, dir = vaultDB(t)
	r, err := Store(db, nil, "", &Memory{Text: "Deploys go out on Tuesdays", Kind: Fact, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	var uid string
	if err := db.QueryRow(`SELECT uid FROM memories WHERE id = ?`, r.ID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	path = memoryFilePath(dir, Fact, uid)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	return db, dir, path, r.ID
}

// An unreadable file confirms nothing about its memory. Counted as found, it
// cleared the unflushed flag of a memory no read had seen in the vault, and
// doctor stopped reporting a row that one more wipe would lose.
func TestAnUnreadableMemoryFileKeepsItsUnflushedFlag(t *testing.T) {
	db, dir, _, id := storeUnreadable(t)
	if _, err := db.Exec(`UPDATE memories SET unflushed = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ImportEdits(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	var unflushed int
	if err := db.QueryRow(`SELECT unflushed FROM memories WHERE id = ?`, id).Scan(&unflushed); err != nil {
		t.Fatalf("the memory behind an unreadable file is gone: %v", err)
	}
	if unflushed != 1 {
		t.Error("an index cleared the unflushed flag of a memory whose file it could not read")
	}
}

// The hand-edit guard compares the file with its stamp. A file it could not
// read was taken as nothing to compare, and the rewrite replaced whatever a
// person had put there.
func TestAMemoryFileThatCannotBeReadIsNotOverwritten(t *testing.T) {
	db, _, path, _ := storeUnreadable(t)
	os.Chmod(path, 0o600)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0)

	// The same text again reinforces the memory, which rewrites its file.
	if _, err := Store(db, nil, "", &Memory{Text: "Deploys go out on Tuesdays", Kind: Fact, Source: "manual"}); err == nil {
		t.Error("a store rewrote a memory file it could not read, with no error")
	}
	os.Chmod(path, 0o600)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the unreadable memory file was replaced")
	}
}
