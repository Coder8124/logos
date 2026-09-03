package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pin state is bookkeeping like confidence or salience, so it has to survive
// the same wipe-and-reimport round trip everything else in the vault does —
// `rm -rf .brain` must not silently un-pin the things a user was relying on.
func TestPinStateSurvivesTheVaultRoundTrip(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "always keep this in context", Kind: Fact, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := Pin(db, m.ID); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, _ := All(wiped)
	if len(all) != 1 || all[0].Pin != PinAlways {
		t.Fatalf("pin state should survive the round trip, got %+v", all)
	}

	// The comment is meant to be legible enough to hand-edit — assert the
	// marker is actually there, not just that a re-import happens to work.
	raw, err := os.ReadFile(filepath.Join(dir, Dir, string(Fact)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pin=1") {
		t.Errorf("exported file should record pin=1 in its bookkeeping comment, got:\n%s", raw)
	}
}

// preePinSchema is what Schema looked like before the pin column existed —
// a vault created before this feature shipped. Init must still open it and
// migrate it in place, per the project's additive/backward-compatible rule.
const prePinSchema = `
CREATE TABLE IF NOT EXISTS memories (
    id            INTEGER PRIMARY KEY,
    text          TEXT NOT NULL,
    kind          TEXT NOT NULL,
    salience      REAL NOT NULL DEFAULT 0.5,
    confidence    REAL NOT NULL DEFAULT 0.7,
    project       TEXT NOT NULL DEFAULT '',
    source        TEXT,
    created       INTEGER NOT NULL,
    last_used     INTEGER NOT NULL DEFAULT 0,
    uses          INTEGER NOT NULL DEFAULT 0,
    vec           BLOB,
    fingerprint   TEXT UNIQUE,
    superseded    INTEGER NOT NULL DEFAULT 0,
    superseded_by INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memories_kind ON memories(kind);
CREATE INDEX IF NOT EXISTS memories_project ON memories(project);
CREATE TABLE IF NOT EXISTS memory_log (
    id     INTEGER PRIMARY KEY,
    ts     INTEGER NOT NULL,
    mem_id INTEGER NOT NULL,
    event  TEXT NOT NULL,
    detail TEXT,
    ref_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memory_log_ts ON memory_log(ts);
`

// A vault indexed before this feature existed must keep working: reopening it
// (via Init, exactly as brain does on every startup) has to succeed, existing
// rows must read back with pin defaulting to PinNone, and the new pin
// operations must work against the now-migrated table.
func TestOldDatabaseWithoutPinColumnStillOpens(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(prePinSchema); err != nil {
		t.Fatal(err)
	}
	// A row written under the old schema, before pin existed at all.
	if _, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, source, created, fingerprint)
		 VALUES (?,?,?,?,?,?,?)`,
		"written before pinning shipped", "fact", 0.5, 0.7, "test", 1, "fp-pre-pin"); err != nil {
		t.Fatal(err)
	}

	// This is what every `brain` startup does — Init must migrate in place,
	// not require a fresh vault.
	if err := Init(db); err != nil {
		t.Fatalf("Init must open a pre-pin database without error, got %v", err)
	}

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("the pre-existing row must survive migration, got %d rows", len(all))
	}
	if all[0].Pin != PinNone {
		t.Errorf("a migrated row with no prior pin state should default to PinNone, got %d", all[0].Pin)
	}

	// The new feature has to actually work against the migrated table, not
	// just fail to error.
	if err := Pin(db, all[0].ID); err != nil {
		t.Fatalf("Pin should work against a migrated database, got %v", err)
	}
	pinned, err := Pinned(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 {
		t.Errorf("Pin/Pinned should work against a migrated database, got %+v", pinned)
	}
}

func TestPinMarksAlwaysInclude(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the standing instruction", Fact, 0.5, []float32{1, 0, 0})
	all, _ := All(db)
	id := all[0].ID

	if err := Pin(db, id); err != nil {
		t.Fatal(err)
	}
	all, _ = All(db)
	if all[0].Pin != PinAlways {
		t.Errorf("Pin should set PinAlways, got %d", all[0].Pin)
	}
}

func TestExcludeMarksNeverInclude(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "something I'd rather not see again", Fact, 0.5, []float32{1, 0, 0})
	all, _ := All(db)
	id := all[0].ID

	if err := Exclude(db, id); err != nil {
		t.Fatal(err)
	}
	all, _ = All(db)
	if len(all) != 1 || all[0].Pin != PinNever {
		t.Errorf("Exclude should set PinNever while leaving the memory on record, got %+v", all)
	}
}

func TestUnpinReturnsToNormalRanking(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "temporarily pinned", Fact, 0.5, []float32{1, 0, 0})
	all, _ := All(db)
	id := all[0].ID

	Pin(db, id)
	Unpin(db, id)
	all, _ = All(db)
	if all[0].Pin != PinNone {
		t.Errorf("Unpin should return to PinNone, got %d", all[0].Pin)
	}

	// Unpin must also undo an exclusion, not just a pin — it is the inverse of
	// either direction, not just the pinned one.
	Exclude(db, id)
	Unpin(db, id)
	all, _ = All(db)
	if all[0].Pin != PinNone {
		t.Errorf("Unpin should undo an exclusion too, got %d", all[0].Pin)
	}
}

func TestSetPinOnUnknownIDIsANoop(t *testing.T) {
	db := testDB(t)
	// Matches Forget's existing behaviour on a bad id: permissive, not an error.
	if err := Pin(db, 999); err != nil {
		t.Errorf("Pin on an unknown id should be a silent no-op, got %v", err)
	}
	if err := Exclude(db, 999); err != nil {
		t.Errorf("Exclude on an unknown id should be a silent no-op, got %v", err)
	}
}

func TestPinnedFuncReturnsOnlyPinAlways(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "always show this", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "never show this", Fact, 0.5, []float32{0, 1, 0})
	storeVec(t, db, "ranked normally", Fact, 0.5, []float32{0, 0, 1})
	all, _ := All(db)
	for _, m := range all {
		switch m.Text {
		case "always show this":
			Pin(db, m.ID)
		case "never show this":
			Exclude(db, m.ID)
		}
	}

	pinned, err := Pinned(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 1 || pinned[0].Text != "always show this" {
		t.Errorf("Pinned should return exactly the PinAlways memories, got %+v", pinned)
	}
}

func TestPinnedRespectsProjectScope(t *testing.T) {
	db := testDB(t)
	db.Exec(`INSERT INTO memories (text, kind, salience, source, created, project, fingerprint, pin)
	          VALUES (?,?,?,?,?,?,?,?)`, "global pin", "fact", 0.5, "test", 1, "", "fp1", PinAlways)
	db.Exec(`INSERT INTO memories (text, kind, salience, source, created, project, fingerprint, pin)
	          VALUES (?,?,?,?,?,?,?,?)`, "kestrel pin", "fact", 0.5, "test", 1, "kestrel", "fp2", PinAlways)
	db.Exec(`INSERT INTO memories (text, kind, salience, source, created, project, fingerprint, pin)
	          VALUES (?,?,?,?,?,?,?,?)`, "orrery pin", "fact", 0.5, "test", 1, "orrery", "fp3", PinAlways)

	got, err := Pinned(db, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("kestrel scope should see its own pin plus the global one, got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Text == "orrery pin" {
			t.Error("a project's pinned scope must not leak another project's pin")
		}
	}
}

// PinNever is exclusion from recall, not from visibility — a user managing
// what's excluded still needs to see it via the CLI listing.
func TestExcludedMemoryStaysVisibleInAll(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "excluded but on record", Fact, 0.5, []float32{1, 0, 0})
	all, _ := All(db)
	Exclude(db, all[0].ID)

	all, _ = All(db)
	if len(all) != 1 {
		t.Fatalf("All must keep listing an excluded memory, got %d", len(all))
	}
}

func TestExcludedMemoryIsExcludedFromNoProviderRecall(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "excluded fact", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "normal fact", Fact, 0.5, []float32{0, 1, 0})
	all, _ := All(db)
	for _, m := range all {
		if m.Text == "excluded fact" {
			Exclude(db, m.ID)
		}
	}

	// Recall/RecallInProject fall back to All/AllInProject when p is nil (no
	// embedding backend) — that fallback must still honour PinNever.
	got, err := Recall(db, nil, "", "query", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Text == "excluded fact" {
			t.Error("an excluded memory must not surface from the no-provider recall fallback")
		}
	}

	got, err = RecallInProject(db, nil, "", "query", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Text == "excluded fact" {
			t.Error("an excluded memory must not surface from the no-provider project recall fallback")
		}
	}
}

func TestExcludedMemoryIsExcludedFromScopedRecall(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "excluded fact", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "normal fact", Fact, 0.5, []float32{1, 0.01, 0})
	all, _ := All(db)
	for _, m := range all {
		if m.Text == "excluded fact" {
			Exclude(db, m.ID)
		}
	}

	got, err := recallByVec(db, []float32{1, 0, 0}, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Text == "excluded fact" {
			t.Error("recallByVec/recallScoped must filter PinNever at the SQL level")
		}
	}
}

func TestExcludedMemoryIsExcludedFromSurface(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "excluded preference", Preference, 0.9, []float32{1, 0, 0})
	storeVec(t, db, "normal preference", Preference, 0.5, []float32{0, 1, 0})
	all, _ := All(db)
	for _, m := range all {
		if m.Text == "excluded preference" {
			Exclude(db, m.ID)
		}
	}

	got, err := Surface(db, []Kind{Preference}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Text == "excluded preference" {
			t.Error("Surface (used by the nightly consolidation pass) must not surface an excluded memory")
		}
	}
}

// excludeNever leaves the slice's identity alone when there is nothing to
// drop — a regression here would silently corrupt All's own backing array
// since it reuses mems[:0] as its scratch space.
func TestExcludeNeverLeavesAnUnaffectedSliceIntact(t *testing.T) {
	mems := []Memory{{ID: 1, Pin: PinNone}, {ID: 2, Pin: PinAlways}}
	got := excludeNever(mems)
	if len(got) != 2 {
		t.Errorf("nothing to exclude, want both kept, got %+v", got)
	}
}
