package memory

import (
	"testing"
)

// Decay used to write the decayed value back as the stored salience, so every
// `logos memory consolidate` re-applied all the decay since last_used to a
// number that already carried it, and ranking decayed the result again.
func TestCountingFadedMemoriesLeavesTheirStoredSalienceAlone(t *testing.T) {
	db := testDB(t)
	now := int64(200 * 86400)
	lastUsed := now - int64(HalfLifeDays*86400)
	if _, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, source, created, last_used, fingerprint) VALUES (?,?,?,?,?,?,?)`,
		"prefers tabs", string(Preference), 1.0, "test", 1, lastUsed, fingerprint("prefers tabs")); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		n, err := Faded(db, now)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("run %d: Faded = %d, want 1 — a memory unused for a half-life has faded", run, n)
		}
	}
	var s float64
	if err := db.QueryRow(`SELECT salience FROM memories`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != 1.0 {
		t.Errorf("stored salience = %.3f after two runs, want 1.000 — decay belongs to EffectiveSalience alone", s)
	}
}
