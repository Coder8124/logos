package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sixth thing only the database knew. `brain memory log` and `brain memory
// diff` read memory_log, and memory_log lived in .brain/index.db alone — so the
// rebuild every document calls safe deleted the history and re-dated what was
// left to the moment of the rebuild. A record of what changed that says
// everything changed today is worse than no record.
func TestTheMemoryTimelineSurvivesDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "the annual toggle belongs in PricingTable", Kind: Fact, Source: "manual", Project: "billing"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := Forget(db, m.ID); err != nil {
		t.Fatal(err)
	}

	before, err := Timeline(db, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("expected a created and a forgotten event, got %d", len(before))
	}

	// The wipe, then the documented-safe rebuild.
	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLog(wiped, dir); err != nil {
		t.Fatal(err)
	}

	after, err := Timeline(wiped, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("timeline had %d events, %d after the rebuild:\n%+v", len(before), len(after), after)
	}
	for i := range before {
		b, a := before[i], after[i]
		if a.TS != b.TS {
			t.Errorf("event %q was re-dated by the rebuild: was %d, now %d", b.Event, b.TS, a.TS)
		}
		if a.Event != b.Event || a.MemID != b.MemID || a.Detail != b.Detail || a.Project != b.Project {
			t.Errorf("event %d came back changed:\n before %+v\n after  %+v", i, b, a)
		}
	}

	// A forgotten memory's line is the one a person most wants, and it is the
	// one a join to memories could never restore.
	raw, err := os.ReadFile(filepath.Join(dir, Dir, LogFile))
	if err != nil {
		t.Fatalf("nothing written to the vault: %v", err)
	}
	if !strings.Contains(string(raw), EvForgotten) {
		t.Errorf("the log file does not record the deletion:\n%s", raw)
	}
}

// Restoring a wiped row is not the same event as creating the memory. It used
// to be logged as a creation stamped with the time of the rebuild, so a vault
// whose log file predates this feature reported every fact as learned today.
func TestRestoringAWipedRowDoesNotBackdateToTheRebuild(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "Priya owns the billing service", Kind: Person, Source: "manual"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	created := m.Created

	// A vault with memory files but no log file at all: every install that
	// predates this feature.
	if err := os.Remove(filepath.Join(dir, Dir, LogFile)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLog(wiped, dir); err != nil {
		t.Fatal(err)
	}

	got, err := Timeline(wiped, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one synthesised creation, got %d: %+v", len(got), got)
	}
	if got[0].TS != created {
		t.Errorf("the memory was recorded as learned at %d; the timeline says %d", created, got[0].TS)
	}
}

// The same re-dating, one path over: restoring a proposal out of pending.md
// logged a fresh "quarantined" event stamped with the rebuild, so a fact an
// agent proposed last week showed up in the timeline as proposed this morning.
func TestARestoredProposalKeepsTheDateItWasProposed(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{Text: "the annual toggle belongs in PricingTable", Kind: Fact, Source: "mcp", Agent: "claude-code", Quarantined: true}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	// Back-dated, because the bug is invisible inside one second: the fresh
	// event the restore logged and the honest one differ only by how long the
	// test took. In the vault this was found in they differed by nine days.
	proposed := m.Created - 9*24*3600
	if _, err := db.Exec("UPDATE memories SET created = ? WHERE id = ?", proposed, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := flushPending(db); err != nil {
		t.Fatal(err)
	}
	// A vault from before log.md existed, which is every vault this repair is
	// for: the proposal is in pending.md and its history is nowhere.
	if err := os.Remove(filepath.Join(dir, Dir, LogFile)); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportPending(wiped, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLog(wiped, dir); err != nil {
		t.Fatal(err)
	}

	got, err := Timeline(wiped, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("timeline has %d events, want 1:\n%+v", len(got), got)
	}
	if got[0].Event != EvQuarantined {
		t.Errorf("event is %q, want %q", got[0].Event, EvQuarantined)
	}
	if got[0].TS != proposed {
		t.Errorf("proposal dated %d, want %d — the rebuild re-dated it", got[0].TS, proposed)
	}
}
