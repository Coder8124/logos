package ops

import (
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// remember refuses a procedure with no route and trap, because one stored
// anyway is never matched by before_you_try again. A correction becomes a
// procedure because the memory it replaces was one, so the same rule applies —
// otherwise "use port 9090" retires a working procedure for a dead one.
func TestACorrectionOfAProcedureMustStillBeAProcedure(t *testing.T) {
	db := openIndexAt(t, t.TempDir())
	old := memory.Memory{Text: "route: deploy with make ship | trap: plain make skips the migration", Kind: memory.Procedure, Source: "manual"}
	if _, err := memory.Store(db, nil, "", &old); err != nil {
		t.Fatal(err)
	}
	if _, err := Correct(db, CorrectRequest{ID: old.ID, Memory: memory.Memory{Text: "use port 9090", Source: "manual"}}); err == nil {
		t.Fatal("a procedure was corrected to text with no route or trap")
	}
	all, _ := memory.All(db)
	kept := false
	for _, m := range all {
		kept = kept || m.ID == old.ID
	}
	if !kept {
		t.Error("a refused correction retired the procedure it was correcting")
	}
	if _, err := Correct(db, CorrectRequest{ID: old.ID, Memory: memory.Memory{
		Text: "route: deploy with make ship-all | trap: make ship skips the assets", Source: "manual"}}); err != nil {
		t.Errorf("a valid procedure correction was refused: %v", err)
	}
}
