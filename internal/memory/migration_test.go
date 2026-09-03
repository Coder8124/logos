package memory

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// A vault created before a column existed must keep working, untouched, the
// moment the binary that added it runs against it. This is the same promise
// every earlier ALTER TABLE in Init already made — for superseded, confidence,
// project, superseded_by — proven the same way: build the table exactly as an
// older brain would have left it, run today's Init over it, and check nothing
// broke and nothing was silently dropped.
//
// There are two such columns to prove, added on separate branches and merged
// here, so there are two tests. They deliberately do not share a schema
// constant: each one pins down the table as it actually stood at its own
// starting point, and a shared constant would drift away from both.

// preAgentSchema is the memories table before Agent, reproduced by hand rather
// than by importing an old version of Schema — the point is to pin down what a
// real installed vault looks like today, which a shared constant could not do
// once this file also changes.
const preAgentSchema = `
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
`

func TestOldDatabaseOpensAfterAgentMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(preAgentSchema); err != nil {
		t.Fatal(err)
	}
	// A row written by the old schema — no agent column exists yet to put
	// anything in.
	if _, err := db.Exec(
		`INSERT INTO memories (text, kind, salience, confidence, project, source, created, fingerprint)
		 VALUES (?,?,?,?,?,?,?,?)`,
		"the BOM target is $38", string(Fact), 0.6, 0.8, "", "manual", 1700000000, fingerprint("the BOM target is $38")); err != nil {
		t.Fatal(err)
	}

	// This is the migration under test: Init must add the column without
	// touching what is already there.
	if err := Init(db); err != nil {
		t.Fatalf("Init on a pre-agent database failed: %v", err)
	}

	all, err := All(db)
	if err != nil {
		t.Fatalf("a pre-migration row could not be read back: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 memory carried over, got %d", len(all))
	}
	if all[0].Agent != "" {
		t.Errorf("a row written before Agent existed should read back empty, got %q", all[0].Agent)
	}
	if all[0].Text != "the BOM target is $38" {
		t.Errorf("the pre-existing row's text changed: got %q", all[0].Text)
	}

	// The migration has to leave the store writable, not just readable.
	if _, err := Store(db, nil, "", &Memory{
		Text: "a new fact after the migration", Kind: Fact, Source: "manual", Agent: "claude-code",
	}); err != nil {
		t.Fatalf("could not store into a migrated database: %v", err)
	}
	all, err = All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 memories after storing into the migrated db, got %d", len(all))
	}

	// Init has to stay idempotent: running it again (as every process start
	// does) must not error on a column that is already there.
	if err := Init(db); err != nil {
		t.Errorf("re-running Init on an already-migrated database failed: %v", err)
	}
}

// The hard constraint quarantine runs on: an index.db written before the
// quarantined column existed must keep opening, keep every memory it already
// held visible, and keep accepting writes. This builds the schema exactly as
// it stood before that change — no quarantined column at all, and a memory_log
// with no project column either — seeds a row by hand, then runs today's Init
// against it, the same way a real user's stale .brain/index.db would be opened
// by a binary built from this tree.
func TestInitMigratesAPreQuarantineDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	const oldSchema = preAgentSchema + `
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
