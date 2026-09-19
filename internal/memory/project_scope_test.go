package memory

import "testing"

// The context pack only admits a global preference, testing `m.Project == ""`.
// activeMemories never selected the project column, so every memory Surface
// returned had an empty one and passed that test — and a preference stated in
// one repository was presented as the user's standing preference in every
// other repository, with no label saying where it came from. That is the
// cross-project leak the README promises against.
func TestASurfacedMemoryCarriesTheProjectItWasFiledUnder(t *testing.T) {
	db := testDB(t)
	m := Memory{Text: "The user prefers tabs over spaces in Go files", Kind: Preference, Project: "kestrel", Source: "test"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	got, err := Surface(db, []Kind{Preference}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one stored preference, got %d", len(got))
	}
	if got[0].Project != "kestrel" {
		t.Errorf("a preference filed under kestrel surfaced as project %q, which reads as global everywhere", got[0].Project)
	}
}

// Confidence is loaded on the same row and was zero for the same reason. It
// multiplies into the recall score, so a memory that arrives with zero
// confidence is one no ranking can lift.
func TestASurfacedMemoryCarriesItsConfidence(t *testing.T) {
	db := testDB(t)
	m := Memory{Text: "the regulator browns out at 3.1V", Kind: Fact, Source: "test"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	got, err := Surface(db, []Kind{Fact}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one stored fact, got %d", len(got))
	}
	if got[0].Confidence == 0 {
		t.Error("a surfaced memory arrived with zero confidence, which no ranking can lift")
	}
}

// Consolidate can supersede a memory on the model's say-so, and superseding is
// not reversible from the vault. Two repositories that deploy on different
// days each hold a true sentence; neither updates the other.
func TestTwoProjectsFactsAreNotConsolidatedAgainstEachOther(t *testing.T) {
	kestrel := Memory{Text: "deploys on Tuesdays", Project: "kestrel"}
	heron := Memory{Text: "deploys on Fridays", Project: "heron"}
	global := Memory{Text: "deploys on Tuesdays"}

	if consolidatable(kestrel, heron) {
		t.Error("two different projects' memories were offered to the classifier as a pair")
	}
	if !consolidatable(kestrel, Memory{Text: "deploys on Wednesdays", Project: "kestrel"}) {
		t.Error("two memories in the same project were not compared")
	}
	// A global memory is about the user, not about one repository, so it stays
	// comparable in both directions.
	if !consolidatable(global, kestrel) || !consolidatable(kestrel, global) {
		t.Error("a global memory was excluded from consolidation against a project's")
	}
}
