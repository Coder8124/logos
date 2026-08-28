package memory

// F1: semantic dedup used to discard distinct facts and report them to the
// caller as reinforcements. These are the regression guards for both halves of
// the behaviour — distinct records must survive, genuine restatements must
// still collapse.

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pragun/brain/internal/provider"
)

// dedupStore is a cache-only store: no vault binding keeps these tests off the
// filesystem, since what is under test is what reaches the database at all.
func dedupStore(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	SetVault(db, "")
	return db
}

// embedder returns a live local embedding model, or skips. The dedup guard only
// runs when there are vectors, so these cases need a real runtime.
func embedder(t *testing.T) (*provider.Provider, string) {
	t.Helper()
	found := provider.Discover()
	if len(found) == 0 {
		t.Skip("no local model runtime; the semantic dedup path needs vectors")
	}
	for _, m := range found[0].Models {
		if strings.Contains(m, "embed") {
			return found[0].Provider, m
		}
	}
	t.Skip("no embedding model available")
	return nil, ""
}

// Semantic dedup collapses a new memory into an existing one at cosine >= 0.87.
// The comment on DedupThreshold says "genuinely distinct facts about the same
// subject are kept; only near-restatements collapse". These are distinct facts
// in the same sentence frame — the shape real records take — and the question is
// whether they survive being written.
func TestDistinctFactsInTheSameFrameAreNotMerged(t *testing.T) {
	p, model := embedder(t)
	db := dedupStore(t)

	facts := []string{
		"invoice 1041 was paid on the 3rd",
		"invoice 1042 was paid on the 4th",
		"invoice 1043 was paid on the 5th",
		"the server in rack 12 is running hot",
		"the server in rack 13 is running hot",
		"the staging database is on port 5432",
		"the staging database is on port 5433",
	}
	var merged []string
	for _, f := range facts {
		r, err := Store(db, p, model, &Memory{Text: f, Kind: Fact, Source: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome == EvReinforced {
			merged = append(merged, f)
		}
	}

	if len(merged) > 0 {
		t.Errorf("%d of %d distinct facts were discarded as restatements of an "+
			"earlier one and reported to the caller as 'reinforced':\n  %s\n"+
			"Nothing recovers these: the text is never stored, so the vault copy "+
			"cannot bring it back either.",
			len(merged), len(facts), strings.Join(merged, "\n  "))
	}

	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(facts) {
		t.Errorf("stored %d of %d facts", len(all), len(facts))
	}
}

// The counterpart the threshold exists for: a genuine restatement must still
// collapse, or the store bloats on every re-extraction.
func TestGenuineRestatementsStillCollapse(t *testing.T) {
	p, model := embedder(t)
	db := dedupStore(t)

	for _, f := range []string{
		"I prefer terse replies with no preamble",
		"I like my replies terse, without any preamble",
		"keep replies terse and skip the preamble",
	} {
		if _, err := Store(db, p, model, &Memory{Text: f, Kind: Preference, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("three phrasings of one preference produced %d memories; the "+
			"dedup guard is not doing the job it costs distinct facts to provide", len(all))
	}
}
