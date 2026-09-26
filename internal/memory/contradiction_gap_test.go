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
		// Found in review: each was queued as a dispute of a fact it agrees with
		// or has nothing to do with. A "not" naming a rejected alternative, a
		// short denial sharing one word with an unrelated memory, a "not" inside
		// a fixed phrase, an article or adverb changed after the copula, and a
		// final full stop.
		{"we use postgres", "we use postgres, not mysql"},
		{"the ci image is built with docker and pushed to ghcr", "we do not use docker"},
		{"the endpoint returns 404 for missing ids", "the endpoint returns 404 not found for missing ids"},
		{"the database is a postgres instance", "the database is the postgres instance"},
		{"the api is really slow", "the api is very slow"},
		{"retries are on", "retries are on."},
		// Found reviewing the review fix: with both "not"s taken out, two
		// denials read as a swap, though neither asserts what the other denies.
		{"the cache is not redis", "the cache is not memcached"},
		{"the api is not slow", "the api is not fast"},
		// A denial with nothing left once its "not" is out denies nothing in
		// particular, so it must not contest whatever it lands beside.
		{"we use docker for local development", "Never."},
		{"retries are enabled", "Don't!"},
	} {
		if textmatch.Reverses(pair[0], pair[1]) {
			t.Errorf("%q was read as reversing %q", pair[1], pair[0])
		}
	}
}

// What the narrowing must keep: a claim denied in any of the usual ways, and a
// short predicate swapped, including at the end of a sentence with a stop.
func TestAClaimDeniedOrSwappedIsStillReadAsAReversal(t *testing.T) {
	for _, pair := range [][2]string{
		{"we deploy staging with kubernetes", "we no longer deploy staging with kubernetes"},
		{"we deploy staging with kubernetes", "we don't deploy staging with kubernetes anymore"},
		{"request retries are enabled in production", "request retries are not enabled in production"},
		{"the staging database is postgres.", "the staging database is mysql."},
		{"retries are on in production", "retries are off in production"},
		// An emphatic "do" is the claim a "do not" denies, and a "do" that is
		// the verb itself must survive being denied.
		{"we do use docker", "we do not use docker"},
		{"we did deploy it", "we didn't deploy it"},
		{"they do the builds", "they don't do the builds"},
		{"we do the builds and do deploy on fridays", "we do the builds and don't deploy on fridays"},
	} {
		if !textmatch.Reverses(pair[0], pair[1]) {
			t.Errorf("%q was not read as reversing %q", pair[1], pair[0])
		}
	}
}
