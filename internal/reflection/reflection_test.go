package reflection

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/memory"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seed inserts a memory row directly, bypassing embedding, so composition and
// confidence stats can be exercised deterministically.
func seed(db *sql.DB, kind, project string, conf float64, uses int) {
	db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, project, source, created, uses)
		 VALUES (?,?,?,?,?,?,?,?)`,
		kind+"-"+project, kind, 0.5, conf, project, "manual", 1, uses)
}

func TestComposeComposition(t *testing.T) {
	db := testDB(t)
	seed(db, "preference", "brain", 0.9, 4) // sure, most exercised
	seed(db, "preference", "", 0.5, 0)      // hunch
	seed(db, "person", "brain", 0.7, 1)

	r, err := Compose(db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 3 {
		t.Fatalf("Total = %d, want 3", r.Total)
	}
	if r.HighConfidence != 1 {
		t.Errorf("HighConfidence = %d, want 1", r.HighConfidence)
	}
	if r.Hypotheses != 1 {
		t.Errorf("Hypotheses = %d, want 1", r.Hypotheses)
	}
	// preference is the most common kind.
	if len(r.ByKind) == 0 || r.ByKind[0].Label != "preference" || r.ByKind[0].N != 2 {
		t.Errorf("ByKind[0] = %+v, want preference/2", r.ByKind)
	}
	// project 'brain' has 2; the empty-project memory is excluded from ByProject.
	if len(r.ByProject) != 1 || r.ByProject[0].Label != "brain" || r.ByProject[0].N != 2 {
		t.Errorf("ByProject = %+v, want [brain 2]", r.ByProject)
	}
	// Most exercised is the 4-use preference; the 0-use one is excluded.
	if len(r.MostExercised) != 2 || r.MostExercised[0].Uses != 4 {
		t.Fatalf("MostExercised = %d entries, top uses %v", len(r.MostExercised), r.MostExercised)
	}
}

func TestComposeGrowthWindow(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	// Two learned this week, one three weeks ago.
	logAt(db, now.Add(-1*24*time.Hour).Unix(), memory.EvCreated)
	logAt(db, now.Add(-2*24*time.Hour).Unix(), memory.EvCreated)
	logAt(db, now.AddDate(0, 0, -21).Unix(), memory.EvCreated)

	r, err := Compose(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Growth) != growthWeeks {
		t.Fatalf("Growth has %d weeks, want %d", len(r.Growth), growthWeeks)
	}
	// The most recent week (last element) should hold the two recent creations.
	last := r.Growth[len(r.Growth)-1]
	if last.Learned != 2 {
		t.Errorf("most recent week learned = %d, want 2", last.Learned)
	}
	total := 0
	for _, w := range r.Growth {
		total += w.Learned
	}
	if total != 3 {
		t.Errorf("growth total = %d, want 3 across the window", total)
	}
}

func logAt(db *sql.DB, ts int64, event string) {
	db.Exec(`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id) VALUES (?,1,?,'',0)`, ts, event)
}
