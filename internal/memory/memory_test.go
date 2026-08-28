package memory

import (
	"database/sql"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// storeVec inserts a memory with a supplied embedding, bypassing the provider so
// recall ranking is testable deterministically.
func storeVec(t *testing.T, db *sql.DB, text string, kind Kind, salience float64, vec []float32) {
	t.Helper()
	_, err := db.Exec(
		`INSERT OR IGNORE INTO memories (text, kind, salience, source, created, vec, fingerprint)
		 VALUES (?,?,?,?,?,?,?)`,
		text, string(kind), salience, "test", 1, floatsToBlob(vec), fingerprint(text))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecallRanksBySimilarity(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "prefers morning meetings", Preference, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "launching in Q4", Context, 0.5, []float32{0, 1, 0})
	storeVec(t, db, "Sarah is the CFO", Person, 0.5, []float32{0, 0, 1})

	// A query pointing along the "meetings" axis should surface that memory.
	got, err := recallByVec(db, []float32{0.9, 0.1, 0}, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Text != "prefers morning meetings" {
		t.Errorf("recall should rank the closest memory first, got %+v", got)
	}
}

func TestSalienceBreaksNearTies(t *testing.T) {
	db := testDB(t)
	// Two memories almost equally close; the more salient should win.
	storeVec(t, db, "trivial", Fact, 0.1, []float32{1, 0.02, 0})
	storeVec(t, db, "important", Fact, 0.95, []float32{1, 0.0, 0})
	got, _ := recallByVec(db, []float32{1, 0, 0}, 2, "")
	if got[0].Text != "important" {
		t.Errorf("salience should break a near tie toward the important memory, got %q", got[0].Text)
	}
}

func TestDedupOnNormalisedText(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "Prefers  short   emails", Preference, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "prefers short emails", Preference, 0.5, []float32{1, 0, 0})
	if n, _ := Count(db); n != 1 {
		t.Errorf("the same fact in different whitespace/case should store once, got %d", n)
	}
}

func TestContextRendersRecalled(t *testing.T) {
	mems := []Memory{
		{Text: "prefers short emails", Kind: Preference},
		{Text: "Sarah is the CFO", Kind: Person},
	}
	ctx := Render(mems)
	for _, want := range []string{"prefers short emails", "Sarah is the CFO", "preference", "person"} {
		if !contains(ctx, want) {
			t.Errorf("context missing %q:\n%s", want, ctx)
		}
	}
	if Render(nil) != "" {
		t.Error("empty memory should render no context")
	}
}

func TestCosineSane(t *testing.T) {
	if math.Abs(cosine([]float32{1, 0}, []float32{1, 0})-1) > 1e-6 {
		t.Error("identical vectors should be 1")
	}
	if cosine([]float32{1, 0}, []float32{0, 1}) != 0 {
		t.Error("orthogonal vectors should be 0")
	}
	if cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched dims must not panic")
	}
}

func TestForget(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "temporary", Fact, 0.5, []float32{1, 0, 0})
	all, _ := All(db)
	if len(all) != 1 {
		t.Fatal("setup")
	}
	Forget(db, all[0].ID)
	if n, _ := Count(db); n != 0 {
		t.Error("forgotten memory should be gone")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEffectiveSalienceDecaysWithAge(t *testing.T) {
	now := int64(1_000_000_000)
	fresh := Memory{Salience: 0.8, Created: now, LastUsed: now}
	old := Memory{Salience: 0.8, Created: now - int64(180*86400), LastUsed: now - int64(180*86400)}
	if EffectiveSalience(fresh, now) <= EffectiveSalience(old, now) {
		t.Error("a fresh memory should outrank an equally-salient stale one")
	}
	// Two half-lives (180d) → ~1/4 of original.
	if e := EffectiveSalience(old, now); e > 0.3 {
		t.Errorf("after ~2 half-lives salience should be ~0.2, got %v", e)
	}
}

func TestReinforcementResistsDecay(t *testing.T) {
	now := int64(1_000_000_000)
	used := Memory{Salience: 0.8, Created: now - int64(90*86400), LastUsed: now - int64(90*86400), Uses: 8}
	unused := Memory{Salience: 0.8, Created: now - int64(90*86400), LastUsed: now - int64(90*86400), Uses: 0}
	if EffectiveSalience(used, now) <= EffectiveSalience(unused, now) {
		t.Error("an often-recalled memory should resist decay better than an unused one")
	}
}

func TestSupersededMemoriesAreNotRecalled(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "lives in NYC", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "lives in Boston", Fact, 0.5, []float32{1, 0.02, 0})
	// Supersede the NYC memory (as Consolidate would on a knowledge update).
	db.Exec("UPDATE memories SET superseded = 1 WHERE text = 'lives in NYC'")

	got, _ := recallByVec(db, []float32{1, 0, 0}, 5, "")
	for _, m := range got {
		if m.Text == "lives in NYC" {
			t.Error("a superseded memory must not be recalled — the current fact should win")
		}
	}
	if len(got) == 0 || got[0].Text != "lives in Boston" {
		t.Errorf("recall should return the current fact, got %+v", got)
	}
}

func TestSurfaceReturnsTopByKind(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "prefers short emails", Preference, 0.9, []float32{1, 0, 0})
	storeVec(t, db, "launching in Q4", Context, 0.6, []float32{0, 1, 0})
	storeVec(t, db, "some trivia", Fact, 0.2, []float32{0, 0, 1})

	prefs, _ := Surface(db, []Kind{Preference, Context}, 5)
	if len(prefs) != 2 {
		t.Fatalf("Surface should return the 2 preference/context memories, got %d", len(prefs))
	}
	if prefs[0].Text != "prefers short emails" {
		t.Errorf("most salient should lead, got %q", prefs[0].Text)
	}
}

func TestHybridRescuesExactToken(t *testing.T) {
	// Two candidates: one is embedding-close to the query but lacks the key
	// token; the other contains the exact identifier. Hybrid must surface the
	// exact-token doc that pure vector would rank lower.
	cands := []Candidate{
		{ID: "a", Text: "we talked about scheduling and calendars in general", Vec: []float32{1, 0.9, 0}},
		{ID: "b", Text: "my confirmation code is ZX9QW7 for the flight", Vec: []float32{1, 0.2, 0}},
	}
	q := "what was my confirmation code ZX9QW7"
	qv := []float32{1, 0.85, 0} // embedding-closer to A
	top := HybridRank(q, qv, cands, 1)
	if len(top) != 1 || top[0] != "b" {
		t.Errorf("hybrid should surface the exact-token doc b, got %v", top)
	}
}

func TestBM25IgnoresStopwordsAndEmptyQuery(t *testing.T) {
	if s := bm25("the and of", []string{"the cat sat", "dog ran"}); s[0] != 0 || s[1] != 0 {
		t.Errorf("an all-stopword query should score nothing, got %v", s)
	}
	scores := bm25("eigenvalue", []string{"eigenvalue decomposition", "unrelated text"})
	if scores[0] <= scores[1] {
		t.Error("the doc containing the term should score higher")
	}
}

func TestNearestMemoryDetectsNearDuplicate(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "prefers short emails", Preference, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "launching in Q4", Context, 0.5, []float32{0, 1, 0})

	// A near-identical vector should find the first memory as a duplicate.
	if _, ok := nearestMemory(db, []float32{0.99, 0.02, 0}, "likes short emails", 0.87); !ok {
		t.Error("a near-identical vector should be flagged as a duplicate")
	}
	// An orthogonal vector should not match anything.
	if _, ok := nearestMemory(db, []float32{0, 0, 1}, "something else entirely", 0.87); ok {
		t.Error("an unrelated vector must not be treated as a duplicate")
	}
	// Close vector, different value asserted: two facts, not one restated.
	storeVec(t, db, "the server in rack 12 is running hot", Fact, 0.5, []float32{0, 0, 1})
	if _, ok := nearestMemory(db, []float32{0, 0, 1}, "the server in rack 13 is running hot", 0.87); ok {
		t.Error("an identical vector must not merge two records that differ by their number")
	}
}

func TestPipelineReportAccuracy(t *testing.T) {
	r := PipelineReport{Cases: 6, RecallHits: 6}
	if r.Accuracy() != 1.0 {
		t.Errorf("accuracy = %v, want 1.0", r.Accuracy())
	}
	if (PipelineReport{}).Accuracy() != 0 {
		t.Error("empty report should be 0, not NaN")
	}
}
