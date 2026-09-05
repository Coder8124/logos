package dream

import "testing"

// A missing model resolves through rt.ModelFor as an error rem already knows
// how to skip on — but a caller that had reduced "no runtime" all the way to
// a nil *router.Router gave rem a nil pointer instead, and rem called
// rt.ModelFor on it with no guard.
func TestRemWithNilRouterSkipsInsteadOfPanicking(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('a','context',0.5,0.7,'manual',1)`)
	db.Exec(`INSERT INTO memories (text, kind, salience, confidence, source, created) VALUES ('b','context',0.5,0.7,'manual',2)`)

	var res Result
	if err := rem(db, nil, false, &res); err != nil {
		t.Fatalf("rem with no model runtime must skip, not error: %v", err)
	}
	if !res.REMSkipped {
		t.Error("REM was reported as having run with no router to run it")
	}
}
