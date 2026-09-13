package memory

import (
	"database/sql"
	"testing"
)

func TestStoreLogsCreationAndSeedsConfidence(t *testing.T) {
	db := testDB(t)
	// nil provider: no embedding, no dedup — just the write path we want to log.
	if _, err := Store(db, nil, "", &Memory{Text: "prefers short emails", Kind: Preference, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	mems, _ := All(db)
	if len(mems) != 1 {
		t.Fatalf("want 1 memory, got %d", len(mems))
	}
	if mems[0].Confidence != 0.9 {
		t.Errorf("manual memory should seed confidence 0.9, got %v", mems[0].Confidence)
	}

	tl, _ := Timeline(db, 10)
	if len(tl) != 1 || tl[0].Event != EvCreated {
		t.Fatalf("creation should be logged as %q, got %+v", EvCreated, tl)
	}
	if tl[0].MemID != mems[0].ID || tl[0].Detail != "prefers short emails" {
		t.Errorf("log entry should point at the memory and snapshot its text, got %+v", tl[0])
	}
}

func TestConfidenceDefaultsBySource(t *testing.T) {
	cases := map[string]float64{"manual": 0.9, "mcp": 0.85, "conversation": 0.6, "": 0.6, "other": 0.7}
	for src, want := range cases {
		if got := defaultConfidence(src); got != want {
			t.Errorf("defaultConfidence(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestForgetIsLoggedAndKeepsHistory(t *testing.T) {
	db := testDB(t)
	Store(db, nil, "", &Memory{Text: "temporary note", Kind: Fact, Source: "manual"})
	id := firstMemID(t, db)

	if err := Forget(db, id); err != nil {
		t.Fatal(err)
	}
	if n, _ := Count(db); n != 0 {
		t.Fatal("memory should be deleted")
	}
	// History survives deletion: created + forgotten, oldest first.
	h, _ := History(db, id)
	if len(h) != 2 || h[0].Event != EvCreated || h[1].Event != EvForgotten {
		t.Errorf("history should record created then forgotten, got %+v", h)
	}
	if h[1].Detail != "temporary note" {
		t.Errorf("forget should snapshot the text before deletion, got %q", h[1].Detail)
	}
}

func TestAllExcludesSuperseded(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "lives in NYC", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "lives in Boston", Fact, 0.5, []float32{1, 0.02, 0})
	db.Exec("UPDATE memories SET superseded = 1, superseded_by = 2 WHERE text = 'lives in NYC'")

	all, _ := All(db)
	if len(all) != 1 || all[0].Text != "lives in Boston" {
		t.Errorf("All should return only the current fact, got %+v", all)
	}
}

func TestRecallScopedFiltersByProject(t *testing.T) {
	db := testDB(t)
	// A global memory and two project memories along the same axis.
	insertProjectMem(t, db, "global preference", "", []float32{1, 0, 0})
	insertProjectMem(t, db, "elysee deploy target is Friday", "elysee", []float32{1, 0.01, 0})
	insertProjectMem(t, db, "logos uses sqlite", "logos", []float32{1, 0.01, 0})

	got, err := recallScoped(db, []float32{1, 0, 0}, 10, "", "elysee", Procedure, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Project != "" && m.Project != "elysee" {
			t.Errorf("scoped recall leaked a %q memory: %q", m.Project, m.Text)
		}
	}
	// The global memory must still be visible inside a project scope.
	if !hasText(got, "global preference") {
		t.Error("project scope should still see global memories")
	}
	if hasText(got, "logos uses sqlite") {
		t.Error("project scope must not see another project's memory")
	}
}

func firstMemID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow("SELECT id FROM memories ORDER BY id LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("no memory found: %v", err)
	}
	return id
}

// insertProjectMem inserts a memory with a project tag and embedding, bypassing
// the provider for deterministic scoped-recall tests.
func insertProjectMem(t *testing.T, db *sql.DB, text, project string, vec []float32) {
	t.Helper()
	_, err := db.Exec(
		`INSERT OR IGNORE INTO memories (text, kind, salience, confidence, project, source, created, vec, fingerprint)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		text, string(Fact), 0.5, 0.7, project, "test", 1, floatsToBlob(vec), fingerprint(text))
	if err != nil {
		t.Fatal(err)
	}
}

func hasText(mems []Memory, text string) bool {
	for _, m := range mems {
		if m.Text == text {
			return true
		}
	}
	return false
}

// The timeline is durable, so an id it records as used stays used across a
// rebuild — including the stretch of the rebuild, or any command run on a fresh
// cache, before memories/log.md has been read back into the table.
//
// Forget the highest-numbered memory and the only place its number survives is
// the log. A new memory stored at that point took the number again, and once
// the log was imported `logos memory history` showed one timeline in which a
// fact was forgotten and then created as something unrelated.
func TestAForgottenIdIsNotReusedBeforeTheTimelineIsRestored(t *testing.T) {
	db, dir := vaultDB(t)
	keep := Memory{Text: "a memory that stays", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &keep); err != nil {
		t.Fatal(err)
	}
	gone := Memory{Text: "a memory that is forgotten", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &gone); err != nil {
		t.Fatal(err)
	}
	if err := Forget(db, gone.ID); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	next := Memory{Text: "an unrelated memory stored after the rebuild", Kind: Fact, Source: "manual"}
	if _, err := Store(wiped, nil, "", &next); err != nil {
		t.Fatal(err)
	}
	if next.ID <= gone.ID {
		t.Fatalf("the new memory took #%d; the timeline already records #%d as a forgotten memory", next.ID, gone.ID)
	}
}
