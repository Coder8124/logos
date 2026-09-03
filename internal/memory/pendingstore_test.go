package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The claim under test is the one every document in this project makes about
// .brain/index.db: it is a cache, deleting it is safe. That was true for notes,
// then for memories, then for working notes — and false for the review queue,
// which is the one place holding facts the user has not agreed to yet.

// Two agents propose something, the user has not reviewed it yet, and the index
// is rebuilt. Before this, both proposals were gone and doctor said "nothing
// pending" — the same report it gives someone who has reviewed everything.
func TestProposalsSurviveDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)

	for _, m := range []Memory{
		{Text: "the annual toggle belongs in PricingTable", Kind: Fact, Source: "mcp", Agent: "claude-code", Quarantined: true},
		{Text: "Priya owns the billing service", Kind: Person, Source: "mcp", Agent: "cursor", Quarantined: true},
	} {
		if _, err := Store(db, nil, "", &m); err != nil {
			t.Fatal(err)
		}
	}

	// The queue is on disk, and it is not in the memory files — a proposal that
	// leaked into fact.md would be quarantine failing in the other direction.
	raw, err := os.ReadFile(filepath.Join(dir, Dir, PendingFile))
	if err != nil {
		t.Fatalf("nothing written to the vault: %v", err)
	}
	if !strings.Contains(string(raw), "PricingTable") {
		t.Errorf("queue file does not hold the proposal:\n%s", raw)
	}
	for _, k := range kinds {
		if b, err := os.ReadFile(filepath.Join(dir, Dir, string(k)+".md")); err == nil {
			if strings.Contains(string(b), "PricingTable") || strings.Contains(string(b), "Priya") {
				t.Errorf("a quarantined memory reached %s.md:\n%s", k, b)
			}
		}
	}

	// The wipe.
	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	n, err := ImportPending(wiped, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("restored %d proposals, want 2", n)
	}

	pend, err := Pending(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 2 {
		t.Fatalf("Pending returned %d, want 2", len(pend))
	}
	// Provenance is the reviewer's evidence. A queue that comes back without
	// who proposed it, or under which kind, is a queue nobody can act on.
	byText := map[string]Memory{}
	for _, m := range pend {
		byText[m.Text] = m
	}
	if got := byText["the annual toggle belongs in PricingTable"]; got.Agent != "claude-code" || got.Kind != Fact {
		t.Errorf("lost provenance: agent=%q kind=%q", got.Agent, got.Kind)
	}
	if got := byText["Priya owns the billing service"]; got.Kind != Person {
		t.Errorf("kind not restored: got %q, want %q", got.Kind, Person)
	}

	// And nothing pending is recallable — the round trip must not have quietly
	// promoted a proposal into memory.
	all, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a restored proposal became an active memory: %+v", all)
	}
}

// Restoring is idempotent. `brain index` runs on a timer in watch mode; a queue
// that doubled on every pass would be worse than one that vanished.
func TestRestoringTheQueueTwiceDoesNotDoubleIt(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "we chose Postgres for the billing service", Kind: Fact, Source: "mcp", Quarantined: true}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	first, err := ImportPending(wiped, dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportPending(wiped, dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 0 {
		t.Fatalf("restored %d then %d, want 1 then 0", first, second)
	}
	pend, _ := Pending(wiped)
	if len(pend) != 1 {
		t.Fatalf("queue holds %d after two imports, want 1", len(pend))
	}
}

// Accepting is the moment a proposal leaves the queue and enters memory. Both
// files have to move, or the next rebuild puts an already-accepted memory back
// in front of the reviewer.
func TestAcceptingAProposalClearsItFromTheQueueFile(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "deploys go out on Tuesdays", Kind: Fact, Source: "mcp", Quarantined: true}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := Accept(db, m.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, Dir, PendingFile)); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(filepath.Join(dir, Dir, PendingFile))
		t.Errorf("queue file survived an empty queue:\n%s", raw)
	}
	raw, err := os.ReadFile(filepath.Join(dir, Dir, string(Fact)+".md"))
	if err != nil {
		t.Fatalf("accepted memory never reached the vault: %v", err)
	}
	if !strings.Contains(string(raw), "deploys go out on Tuesdays") {
		t.Errorf("fact.md does not hold the accepted memory:\n%s", raw)
	}
}

// Rejecting has to reach the file too. The queue is the only record that
// survives the cache, so a rejection left only in the database means the
// memory the user threw out comes back on the next reindex.
func TestARejectedProposalDoesNotComeBack(t *testing.T) {
	db, dir := vaultDB(t)
	keep := Memory{Text: "the staging cluster is us-east-2", Kind: Fact, Source: "mcp", Quarantined: true}
	drop := Memory{Text: "the user wants email notifications", Kind: Preference, Source: "mcp", Quarantined: true}
	for _, m := range []*Memory{&keep, &drop} {
		if _, err := Store(db, nil, "", m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Reject(db, drop.ID); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := ImportPending(wiped, dir); err != nil {
		t.Fatal(err)
	}
	pend, _ := Pending(wiped)
	if len(pend) != 1 {
		t.Fatalf("queue holds %d after one rejection, want 1", len(pend))
	}
	if pend[0].Text != keep.Text {
		t.Errorf("wrong proposal survived: %q", pend[0].Text)
	}
}

// The file says "deleting a line rejects it". It has to be true, because the
// same sentence is true of every other file this product writes.
func TestDeletingALineFromTheQueueRejectsIt(t *testing.T) {
	db, dir := vaultDB(t)
	for _, m := range []Memory{
		{Text: "the release train is weekly", Kind: Fact, Source: "mcp", Quarantined: true},
		{Text: "Sam prefers async reviews", Kind: Person, Source: "mcp", Quarantined: true},
	} {
		if _, err := Store(db, nil, "", &m); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(dir, Dir, PendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "Sam prefers async reviews") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ImportPending(db, dir); err != nil {
		t.Fatal(err)
	}
	pend, _ := Pending(db)
	if len(pend) != 1 || pend[0].Text != "the release train is weekly" {
		t.Fatalf("hand deletion did not reject: %+v", pend)
	}
}

// A missing file is not an empty queue. A partial restore, or a vault written
// before this file existed, must not be read as "the user rejected everything".
func TestAMissingQueueFileRejectsNothing(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "the API gateway terminates TLS", Kind: Fact, Source: "mcp", Quarantined: true}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, Dir, PendingFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPending(db, dir); err != nil {
		t.Fatal(err)
	}
	pend, _ := Pending(db)
	if len(pend) != 1 {
		t.Fatalf("a missing file emptied the queue: %d pending", len(pend))
	}
}

// A write cut off mid-record is an interrupted write, not an edit. Acting on it
// would reject every proposal the missing tail held.
func TestATruncatedQueueIsRefusedRatherThanActedOn(t *testing.T) {
	db, dir := vaultDB(t)
	for _, m := range []Memory{
		{Text: "the ledger is append-only", Kind: Fact, Source: "mcp", Quarantined: true},
		{Text: "invoices are immutable once sent", Kind: Fact, Source: "mcp", Quarantined: true},
	} {
		if _, err := Store(db, nil, "", &m); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, Dir, PendingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.TrimRight(string(raw), "\n")
	cut = cut[:len(cut)-20] // ends inside the last record's comment
	if err := os.WriteFile(path, []byte(cut), 0o600); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	SetVault(wiped, dir)
	t.Cleanup(func() { SetVault(wiped, "") })
	if _, err := ImportPending(wiped, dir); err == nil {
		t.Fatal("a truncated queue was imported without complaint")
	}
	pend, _ := Pending(wiped)
	if len(pend) != 0 {
		t.Errorf("a refused import still wrote rows: %+v", pend)
	}
}

// An unbound database — the app before a vault is chosen — still takes
// proposals. Failing to write a file nobody asked for is not an error.
func TestAnUnboundDatabaseStillTakesProposals(t *testing.T) {
	db := testDB(t)
	m := Memory{Text: "nothing to write to yet", Kind: Fact, Source: "mcp", Quarantined: true}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	pend, _ := Pending(db)
	if len(pend) != 1 {
		t.Fatalf("proposal lost with no vault bound: %d pending", len(pend))
	}
}
