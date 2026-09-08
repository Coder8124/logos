package memory

import (
	"strings"
	"testing"
)

// A procedure is a Memory row like any other kind, so it inherits durability,
// confidence, salience and pinning for free — but it must never reach the two
// surfaces built for facts about the user: ordinary recall (which feeds the
// context pack) and Pinned (which bypasses ranking entirely). Its only reader
// is RecallProcedures, via before_you_try.

func TestAProcedureDoesNotSurfaceInOrdinaryRecall(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "route: run the chaos tier before calling a durability fix done | "+
		"trap: go test ./... passes with the bug present", Procedure, 0.9, []float32{1, 0, 0})
	storeVec(t, db, "prefers morning meetings", Preference, 0.5, []float32{1, 0, 0})

	got, err := recallByVec(db, []float32{1, 0, 0}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Kind == Procedure {
			t.Errorf("a procedure surfaced in ordinary recall: %q", m.Text)
		}
	}

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("All is a curation surface and must still see the procedure, got %d rows", len(all))
	}
}

// The project-scoped path has the same fallback (no embedder) and vector arms
// as the global one, and both need the same guarantee.
func TestAProcedureDoesNotSurfaceInProjectScopedRecall(t *testing.T) {
	db := testDB(t)
	insertProjectMem(t, db, "route: warm the cache before launch | trap: cold cache times out",
		"brain", []float32{1, 0, 0})
	storeVec(t, db, "brain uses sqlite", Fact, 0.5, []float32{1, 0, 0})
	db.Exec("UPDATE memories SET kind = ? WHERE text LIKE 'route:%'", string(Procedure))
	db.Exec("UPDATE memories SET project = 'brain' WHERE text LIKE 'route:%'")

	got, err := RecallInProject(db, nil, "", "warm the cache before launch", "brain", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Kind == Procedure {
			t.Errorf("a procedure surfaced in project-scoped recall: %q", m.Text)
		}
	}
}

// A pinned procedure must not bypass recall's exclusion through Pinned, which
// contextpack reads unconditionally — the one path that does not go through
// recallScoped's ranking at all.
func TestAPinnedProcedureIsNotReturnedByPinned(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "route: rebuild the index after a schema change | trap: stale rows linger silently",
		Procedure, 0.9, []float32{1, 0, 0})
	db.Exec("UPDATE memories SET pin = ? WHERE kind = ?", PinAlways, string(Procedure))

	pinned, err := Pinned(db, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range pinned {
		if m.Kind == Procedure {
			t.Errorf("a pinned procedure leaked through Pinned: %q", m.Text)
		}
	}
}

// RecallProcedures is the mirror: it must see nothing but procedures, and
// nothing outside the vault's whole span (no project scoping), the same
// "search everywhere" property deadend.Collect has for the same reason.
func TestRecallProceduresSeesOnlyProcedures(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "route: run the chaos tier before calling a durability fix done | "+
		"trap: go test ./... passes with the bug present", Procedure, 0.9, []float32{1, 0, 0})
	storeVec(t, db, "prefers morning meetings", Preference, 0.5, []float32{1, 0, 0})

	got, err := RecallProcedures(db, nil, "", "chaos tier", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != Procedure {
		t.Fatalf("want exactly the one procedure, got %+v", got)
	}
}

// Invariant 1, applied to the fifth kind: rm -rf .brain and reindex must not
// lose a procedure, or adding this layer repeats the exact bug that made
// memories files in the first place.
func TestProceduresSurviveDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)

	m := Memory{
		Text: "route: run the chaos tier before calling a durability fix done | " +
			"trap: go test ./... passes with the bug present because the chaos tier is behind a build tag | " +
			"verify: go test -count=1 -tags chaos ./chaos/... | layer: implementation | scope: local | evidence: verified",
		Kind: Procedure, Source: "manual",
	}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}

	// The wipe. A fresh database, as if .brain/index.db had been deleted.
	wiped := testDB(t)
	n, err := Import(wiped, nil, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 memory restored, got %d", n)
	}

	got, err := RecallProcedures(wiped, nil, "", "durability fix", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the procedure did not survive the round trip, got %d", len(got))
	}
	if got[0].ID == 0 {
		t.Error("a restored procedure should keep a real id")
	}
	if !strings.Contains(got[0].Text, "chaos tier") {
		t.Errorf("procedure text did not round-trip: %q", got[0].Text)
	}
}
