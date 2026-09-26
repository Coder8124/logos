package memory

import (
	"testing"

	"github.com/Coder8124/logos/internal/textmatch"
)

// A contradiction is only ever detected when both statements hold a number.
// contestedMemory gates on textmatch.DifferingFactValues, and Values reads
// digits and nothing else, so a dispute stated in words is never a dispute.
// The fake runtime embeds every text at the same point, which isolates the
// text rules: they decide the outcome once two memories sit above
// DedupThreshold. Measured under nomic-embed-text, the negation (0.896) and the
// antonym (0.951) do; the database pair (0.801) does not, so under a real
// runtime it would also need the threshold to let it through.

// "postgres" against "mysql" is the 8080-against-9090 case with a name in place
// of a port. DifferentSubjects reads it as two parallel facts, so both go
// active and the next agent is handed both without being told they disagree.
func TestAFactThatNamesADifferentValueInWordsWaitsForTheUser(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	first, err := Store(db, p, "m", &Memory{
		Text: "the staging database is postgres", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, p, "m", &Memory{
		Text: "the staging database is mysql", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() || r.Contested != first.ID {
		t.Errorf("a fact naming a different database must wait for review against #%d, got %q contested #%d",
			first.ID, r.Outcome, r.Contested)
	}
}

// The worst of the three. "no" and "longer" are not subject words, so the
// negation reads as a restatement and is folded into the fact it reverses:
// the receipt says "reinforced", the reversal is never written, and the old
// fact gains confidence.
func TestANegatedFactIsNotFoldedIntoTheFactItReverses(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	first, err := Store(db, p, "m", &Memory{
		Text: "we deploy staging with kubernetes", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, p, "m", &Memory{
		Text: "we no longer deploy staging with kubernetes", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome == EvReinforced {
		t.Fatalf("the reversal was merged into memory #%d as corroboration of the fact it reverses", r.ID)
	}
	if !r.Queued() || r.Contested != first.ID {
		t.Errorf("a reversal must wait for review against #%d, got %q contested #%d",
			first.ID, r.Outcome, r.Contested)
	}
}

// An antonym is a different value with no digit in it.
func TestAFactThatFlipsASettingWaitsForTheUser(t *testing.T) {
	db := testDB(t)
	p := embedRuntime(t, false)

	first, err := Store(db, p, "m", &Memory{
		Text: "request retries are enabled in production", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, p, "m", &Memory{
		Text: "request retries are disabled in production", Kind: Fact, Source: "mcp", ReviewIfContested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Queued() || r.Contested != first.ID {
		t.Errorf("a flipped setting must wait for review against #%d, got %q contested #%d",
			first.ID, r.Outcome, r.Contested)
	}
}

// The counterpart: what reads like a reversal but is not one. A "no" that
// denies a noun, a word swapped outside the predicate — two parallel facts,
// which DifferentSubjects exists to keep — and an alternative named alongside
// the original all still assert what the first statement did.
func TestWordsThatOnlyLookLikeAReversalAreNotOne(t *testing.T) {
	for _, pair := range [][2]string{
		{"I prefer terse replies with no preamble", "I like my replies terse, without any preamble"},
		{"kestrel handles billing through a dedicated service", "kestrel handles search through a dedicated service"},
		{"we deploy staging with kubernetes", "we deploy staging with kubernetes instead of nomad"},
		{"the cache ttl is 30 seconds", "the cache ttl is 60 seconds"},
	} {
		if textmatch.Reverses(pair[0], pair[1]) {
			t.Errorf("%q was read as reversing %q", pair[1], pair[0])
		}
	}
}
