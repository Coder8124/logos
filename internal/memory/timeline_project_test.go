package memory

import (
	"database/sql"
	"testing"
)

func projectTimelineDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testDB(t)
	return db
}

func storeIn(t *testing.T, db *sql.DB, project, text string) int64 {
	t.Helper()
	r, err := Store(db, nil, "", &Memory{Text: text, Kind: Fact, Project: project, Confidence: 0.8})
	if err != nil {
		t.Fatalf("store %q: %v", text, err)
	}
	if r.ID == 0 {
		return r.Ref
	}
	return r.ID
}

func TestTimelineIsScopedToOneProject(t *testing.T) {
	db := projectTimelineDB(t)
	storeIn(t, db, "kestrel", "the retry budget is per-host")
	storeIn(t, db, "harrier", "the build runs on arm only")
	storeIn(t, db, "kestrel", "auth tokens live for an hour")

	got, err := TimelineInProject(db, "kestrel", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 kestrel events, got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Project != "kestrel" {
			t.Errorf("event %d leaked from project %q", e.ID, e.Project)
		}
	}
}

// The whole reason the project is copied onto the log line instead of joined to
// at read time. A join loses this event, because the row it would join to is
// gone — and "what did I forget" is the single most useful thing in a timeline.
func TestForgottenMemoryStaysInItsProjectTimeline(t *testing.T) {
	db := projectTimelineDB(t)
	id := storeIn(t, db, "kestrel", "the staging port is 8080")
	if err := Forget(db, id); err != nil {
		t.Fatal(err)
	}

	got, err := TimelineInProject(db, "kestrel", 0)
	if err != nil {
		t.Fatal(err)
	}
	var forgotten *LogEntry
	for i := range got {
		if got[i].Event == EvForgotten {
			forgotten = &got[i]
		}
	}
	if forgotten == nil {
		t.Fatalf("the forget event vanished from the project timeline: %+v", got)
	}
	if forgotten.Detail != "the staging port is 8080" {
		t.Errorf("forget event lost its snapshot: %q", forgotten.Detail)
	}
}

// The limit has to be applied after the narrowing. If it is applied before,
// asking for the last N in a quiet project returns the busy project's events
// filtered down to nothing, which reads as "this project has no history".
func TestLimitAppliesAfterTheProjectNarrowing(t *testing.T) {
	db := projectTimelineDB(t)
	storeIn(t, db, "kestrel", "the one kestrel fact")
	for i := 0; i < 10; i++ {
		storeIn(t, db, "harrier", "harrier fact "+itoa(i))
	}

	got, err := TimelineInProject(db, "kestrel", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the 1 kestrel event, got %d", len(got))
	}
}

func TestUnscopedTimelineStillSeesEverything(t *testing.T) {
	db := projectTimelineDB(t)
	storeIn(t, db, "kestrel", "a")
	storeIn(t, db, "harrier", "b")
	storeIn(t, db, "", "c")

	got, err := Timeline(db, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Fatalf("want at least 3 events across all projects, got %d", len(got))
	}
}

func TestUnattributedCountReportsThePartialPast(t *testing.T) {
	db := projectTimelineDB(t)
	storeIn(t, db, "kestrel", "attributed")
	// A line as it would have been written before the column existed.
	if _, err := db.Exec(
		`INSERT INTO memory_log (ts, mem_id, event, detail, ref_id, project) VALUES (1,999,?,'old',0,'')`,
		EvCreated); err != nil {
		t.Fatal(err)
	}
	n, err := UnattributedCount(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 unattributed event, got %d", n)
	}
}

// An old store, opened by today's binary, must migrate rather than fail — and
// the backfill must attribute the events whose memories are still there.
func TestOldLogMigratesAndBackfills(t *testing.T) {
	db := projectTimelineDB(t)
	id := storeIn(t, db, "kestrel", "survives the migration")

	// Simulate the pre-migration state: the column exists but was never filled.
	if _, err := db.Exec("UPDATE memory_log SET project = ''"); err != nil {
		t.Fatal(err)
	}
	if err := Init(db); err != nil {
		t.Fatalf("re-init must be idempotent: %v", err)
	}

	got, err := TimelineInProject(db, "kestrel", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatalf("backfill did not attribute the events of memory #%d", id)
	}
}
