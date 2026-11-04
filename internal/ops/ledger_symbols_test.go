package ops

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	_ "modernc.org/sqlite"
)

// Resume reads a symbol-named project's pre-#368 history from the folder the
// old rule chose; built from SafeScope, the ledger read the new, empty one and
// showed nothing for the same project.
func TestTheLedgerReadsTheHistoryResumeShows(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(db, dir, &session.Checkpoint{Project: "c", Decisions: []string{"keep it, because"}}); err != nil {
		t.Fatal(err)
	}
	l, err := WriteLedger(dir, "C++", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Checkpoints != 1 || l.InForce != 1 {
		t.Errorf("ledger for C++ read %d checkpoints, %d decisions; want 1, 1", l.Checkpoints, l.InForce)
	}
}
