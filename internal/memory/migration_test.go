package memory

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// The hard constraint quarantine runs on: an index.db written before the
// quarantined column existed must keep opening, keep every memory it already
// held visible, and keep accepting writes. This builds the schema exactly as
// it stood the commit before this one — no quarantined column at all — seeds
// a row by hand, then runs today's Init against it, the same way a real
// user's stale .brain/index.db would be opened by a binary built from this
// tree.
func TestInitMigratesAPreQuarantineDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	const oldSchema = `
CREATE TABLE memories (
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
CREATE TABLE memory_log (
    id     INTEGER PRIMARY KEY,
    ts     INTEGER NOT NULL,
    mem_id INTEGER NOT NULL,
    event  TEXT NOT NULL,
    detail TEXT,
    ref_id INTEGER NOT NULL DEFAULT 0
);`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, project, source, created, fingerprint)
		 VALUES (?,?,?,?,?,?,?,?)`,
		"the BOM target is $38", "fact", 0.6, 0.9, "", "manual", 1700000000, fingerprint("the BOM target is $38"),
	); err != nil {
		t.Fatal(err)
	}

	// This is the migration under test: a database with no quarantined column
	// must open, and must gain the column, without disturbing what was
	// already there.
	if err := Init(db); err != nil {
		t.Fatalf("Init did not migrate a pre-quarantine database: %v", err)
	}

	all, err := All(db)
	if err != nil {
		t.Fatalf("All failed against a migrated database: %v", err)
	}
	if len(all) != 1 || all[0].Text != "the BOM target is $38" {
		t.Fatalf("a pre-existing memory was lost or hidden by the migration: %+v", all)
	}
	if all[0].Quarantined {
		t.Error("a pre-existing memory came back quarantined; old rows must default to not-quarantined")
	}

	if n, err := PendingCount(db); err != nil || n != 0 {
		t.Errorf("PendingCount on a freshly migrated database = %d, %v; want 0, nil", n, err)
	}

	// The migrated database must accept new writes exactly as before.
	if _, err := Store(db, nil, "", &Memory{Text: "a new fact", Kind: Fact, Source: "test"}); err != nil {
		t.Fatalf("Store failed on a migrated database: %v", err)
	}
}

// Running Init twice — once on a database that already has the column — must
// stay a no-op, the same guarantee every other migration in this file makes.
func TestInitIsIdempotentOnAnAlreadyMigratedDatabase(t *testing.T) {
	db := testDB(t)
	if err := Init(db); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	if _, err := Store(db, nil, "", &Memory{Text: "still works", Kind: Fact, Source: "test"}); err != nil {
		t.Fatalf("Store failed after a repeated Init: %v", err)
	}
}
