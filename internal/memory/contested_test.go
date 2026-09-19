package memory

import (
	"strings"
	"testing"
)

// Quarantining every agent write made the queue the only way a memory ever
// became real, and MCP is the only path an agent has. Nothing an agent learned
// reached the next agent until the user personally ran `logos review`, which is
// the one thing they installed this to avoid doing. An ordinary new fact must
// be usable the moment it is stored.
func TestAFactNothingContradictsIsActiveWithoutWaitingForReview(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	r, err := Store(db, p, "m", &Memory{
		Text: "staging runs on port 8080", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Created() {
		t.Fatalf("a first fact has nothing to contradict and must go active, got %q", r.Outcome)
	}

	n, err := PendingCount(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("nothing should be waiting for review, got %d", n)
	}
}

// The contradiction is the one case a machine cannot settle: the agent can say
// the port is 9090, but not whether that replaces 8080 or is simply wrong.
func TestAFactThatContradictsAStoredOneWaitsForTheUser(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	first, err := Store(db, p, "m", &Memory{
		Text: "staging runs on port 8080", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	r, err := Store(db, p, "m", &Memory{
		Text: "staging runs on port 9090", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() {
		t.Fatalf("a fact disputing a stored one must wait for review, got %q", r.Outcome)
	}
	// Invariant 3: the receipt carries the number, so the agent can raise the
	// dispute in the conversation instead of leaving it in a queue.
	if r.Contested != first.ID {
		t.Errorf("the receipt should name memory #%d as the one in dispute, got #%d", first.ID, r.Contested)
	}
	if !strings.Contains(r.ContestedText, "8080") {
		t.Errorf("the receipt should quote the memory in dispute, got %q", r.ContestedText)
	}
}

// Two facts about different things are not in dispute, however close their
// vectors sit. This is the case that makes the rule worth having: !sameFact
// would call these contested and put nearly every write back in the queue.
func TestTwoFactsAboutDifferentSubjectsAreNotInDispute(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	if _, err := Store(db, p, "m", &Memory{
		Text: "kestrel handles checkout through a dedicated service", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	}); err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, p, "m", &Memory{
		Text: "kestrel handles pricing through a dedicated service", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Queued() {
		t.Error("a parallel fact about another subject is a new fact, not a contradiction")
	}
}

// Someone who wants the old behaviour still has it, and it must not be
// weakened by the contested rule sitting beside it.
func TestAForcedQuarantineStillQueuesAnUncontestedFact(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	r, err := Store(db, p, "m", &Memory{
		Text: "the release train is weekly", Kind: Fact, Source: "mcp",
		Quarantined: true, ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() {
		t.Fatalf("LOGOS_REVIEW_ALL must still queue everything, got %q", r.Outcome)
	}
}

// Half the pitch is that no model is required, so the guard cannot depend on
// one. With no embedding runtime the contradiction still has to be caught
// lexically, or every dispute auto-accepts for exactly the users who took the
// "no AI runtime required" line at its word.
func TestAContradictionIsCaughtWithNoEmbeddingRuntime(t *testing.T) {
	db := testDB(t)

	first, err := Store(db, nil, "", &Memory{
		Text: "staging runs on port 8080", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created() {
		t.Fatalf("the first fact has nothing to contradict, got %q", first.Outcome)
	}

	r, err := Store(db, nil, "", &Memory{
		Text: "staging runs on port 9090", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() {
		t.Fatalf("without a model the dispute must still be caught, got %q", r.Outcome)
	}
	if r.Contested != first.ID {
		t.Errorf("the receipt should name memory #%d, got #%d", first.ID, r.Contested)
	}
}

// The lexical arm must not fire on two facts that merely both contain numbers.
func TestUnrelatedFactsWithDifferentNumbersAreNotADispute(t *testing.T) {
	db := testDB(t)

	if _, err := Store(db, nil, "", &Memory{
		Text: "staging runs on port 8080", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	}); err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, nil, "", &Memory{
		Text: "the billing invoice batch runs on the 1st of each month", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Queued() {
		t.Errorf("two unrelated facts are not a dispute just because both hold numbers, got %q", r.Outcome)
	}
}
