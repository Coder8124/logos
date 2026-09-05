package project

import (
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

// AutoScope's write loop ignored db.Exec's error entirely and always returned
// len(updates) as the count — so a write that could not happen was reported
// as a scope that succeeded. A trigger that blocks exactly the UPDATE
// AutoScope issues, while leaving every read it depends on untouched, proves
// the write actually failed rather than just not matching anything.
func TestAutoScopeReportsAWriteFailureInsteadOfClaimingSuccess(t *testing.T) {
	db := testDB(t)
	seedProject(t, db)
	memory.Store(db, nil, "", &memory.Memory{Text: "Atlas ships Friday", Kind: memory.Context, Source: "manual"})

	if _, err := db.Exec(`CREATE TRIGGER block_project_update
		BEFORE UPDATE OF project ON memories
		BEGIN SELECT RAISE(ABORT, 'blocked for test'); END`); err != nil {
		t.Fatal(err)
	}

	n, err := AutoScope(db)
	if err == nil {
		t.Fatalf("a write that could not happen was reported as %d successful scope(s)", n)
	}
}
