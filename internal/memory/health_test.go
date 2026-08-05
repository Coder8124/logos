package memory

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func healthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedVec(db *sql.DB, text string, conf, sal float64, created int64, uses int, vec []float32) {
	db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, source, created, uses, vec)
		 VALUES (?,?,?,?,?,?,?,?)`,
		text, "fact", sal, conf, "manual", created, uses, floatsToBlob(vec))
}

func TestHealthFlagsDefects(t *testing.T) {
	db := healthDB(t)
	now := time.Now().Unix()
	old := now - 200*86400

	seedVec(db, "dup a", 0.9, 0.5, now, 1, []float32{1, 0, 0})
	seedVec(db, "dup b", 0.9, 0.5, now, 1, []float32{1, 0, 0})    // identical vec → duplicate
	seedVec(db, "hunch", 0.4, 0.5, now, 1, []float32{0, 1, 0})    // low confidence
	seedVec(db, "old fact", 0.9, 0.1, old, 0, []float32{0, 0, 1}) // stale: old, unused, faded
	seedVec(db, "clean", 0.9, 0.6, now, 1, []float32{1, 1, 1})    // no defect

	rep, err := Health(db)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 5 {
		t.Fatalf("Total = %d, want 5", rep.Total)
	}
	if rep.Duplicates != 2 || len(rep.DuplicatePairs) != 1 {
		t.Errorf("Duplicates = %d, pairs = %d; want 2 / 1", rep.Duplicates, len(rep.DuplicatePairs))
	}
	if rep.LowConfidence != 1 {
		t.Errorf("LowConfidence = %d, want 1", rep.LowConfidence)
	}
	if rep.Stale != 1 {
		t.Errorf("Stale = %d, want 1", rep.Stale)
	}
	// Four distinct memories flagged out of five → 20% clean.
	if rep.Score < 0.19 || rep.Score > 0.21 {
		t.Errorf("Score = %.3f, want ~0.20", rep.Score)
	}
}

func TestHealthEmptyStoreIsPerfect(t *testing.T) {
	db := healthDB(t)
	rep, err := Health(db)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 0 || rep.Score != 1 {
		t.Fatalf("empty store: total=%d score=%.2f, want 0 / 1.00", rep.Total, rep.Score)
	}
}
