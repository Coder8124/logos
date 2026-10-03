package textmatch

import "testing"

// These pin the examples the package's comments give as the reason for each
// rule. Every one of them is a case that went wrong once, in memory dedup,
// conflict detection or dead-end matching, and the consumers' own tests only
// reach them through a model-free path that happens to call in.

func TestSubjectKeepsOnlyTheDistinctiveWords(t *testing.T) {
	got := Subject("What should I do about the waveguide quote for 2026?")
	for _, w := range []string{"waveguide", "quote"} {
		if !got[w] {
			t.Errorf("Subject dropped %q: %v", w, got)
		}
	}
	for _, w := range []string{"what", "should", "about", "the", "2026", "for"} {
		if got[w] {
			t.Errorf("Subject kept %q, which says nothing about the subject: %v", w, got)
		}
	}
}

func TestAkinMatchesInflectionsButNotShortWords(t *testing.T) {
	for _, p := range [][2]string{{"manufacture", "manufacturer"}, {"proposal", "proposals"}, {"quote", "quoted"}} {
		if !Akin(p[0], p[1]) {
			t.Errorf("Akin(%q, %q) = false, want the same term", p[0], p[1])
		}
	}
	// Under five characters a shared prefix is a coincidence, not a stem.
	if Akin("cart", "carton") {
		t.Error(`Akin("cart", "carton") = true, want false`)
	}
}

// Jaccard scored this pair at 0.29, so a superseded price was handed over as
// current. Containment asks whether the shorter statement is about the same
// thing, and it is.
func TestOverlapIsContainmentSoALongerStatementIsNotPunished(t *testing.T) {
	a := Subject("we are targeting a $199 retail price")
	b := Subject("final call: retail price is $249, that is locked for launch")
	if got := Overlap(a, b); got < Related {
		t.Errorf("Overlap = %.2f, want at least %.2f", got, Related)
	}
	if got := Overlap(a, map[string]bool{}); got != 0 {
		t.Errorf("Overlap with an empty side = %.2f, want 0", got)
	}
}

func TestValuesNormaliseMoneyAndUnits(t *testing.T) {
	got := Values("the run is $1,200 over 3 weeks at 15%.")
	for _, v := range []string{"1200", "3 weeks", "15%"} {
		if !got[v] {
			t.Errorf("Values missed %q: %v", v, got)
		}
	}
}

// Zero is not a claim to the conflict detector, and is one to dedup: "retries
// at 0" and "retries at 3" are two decisions.
func TestZeroCountsAsAValueOnlyWhenDecidingWhetherTwoFactsAreOne(t *testing.T) {
	a, b := "retries at 0", "retries at 3"
	if DifferingValues(a, b) {
		t.Error("DifferingValues read 0 as a claim")
	}
	if !DifferingFactValues(a, b) {
		t.Error("DifferingFactValues merged retries at 0 into retries at 3")
	}
	if DifferingValues("price is $249", "price is $249 and locked") {
		t.Error("two statements sharing their value were read as a contradiction")
	}
}

func TestDifferentSubjectsKeepsParallelFactsAndMergesRestatements(t *testing.T) {
	if !DifferentSubjects("kestrel handles checkout through a dedicated service",
		"kestrel handles pricing through a dedicated service") {
		t.Error("checkout and pricing read as one fact, so one of them would be destroyed")
	}
	if DifferentSubjects("I prefer terse replies with no preamble",
		"I like my replies terse, without any preamble") {
		t.Error("a restatement that only adds words read as a second fact")
	}
	// The short names are what tell developer facts apart.
	if !DifferentSubjects("deploys go out through the web build", "deploys go out through the ios build") {
		t.Error("web and ios read as the same subject")
	}
}

func TestNegatedCatchesAPlanBeingCalledOff(t *testing.T) {
	if !Negated("We decided against Kubernetes") {
		t.Error("decided against was not read as calling something off")
	}
	if Negated("deploy on Friday") {
		t.Error("a plain plan read as called off")
	}
}

func TestReversesCatchesADenialOrASwappedPredicate(t *testing.T) {
	for _, p := range [][2]string{
		{"the staging database is postgres", "the staging database is mysql"},
		{"retries are enabled", "retries are disabled"},
		{"we use docker", "we don't use docker"},
		{"we do use docker", "we do not use docker"},
		{"the cache is redis", "the cache is no longer redis"},
	} {
		if !Reverses(p[0], p[1]) || !Reverses(p[1], p[0]) {
			t.Errorf("Reverses(%q, %q) = false, want a reversal both ways", p[0], p[1])
		}
	}
}

func TestReversesLeavesAgreementsAndParallelFactsAlone(t *testing.T) {
	for _, p := range [][2]string{
		// A "not" naming the rejected alternative agrees with the fact.
		{"the database is postgres", "the database is postgres, not mysql"},
		// Two denials can both be true.
		{"the cache is not redis", "the cache is not memcached"},
		// A modifier changed, not the value.
		{"the database is a postgres instance", "the database is the postgres instance"},
		// A swapped subject is a second fact, which DifferentSubjects keeps.
		{"kestrel handles billing through a dedicated service", "kestrel handles search through a dedicated service"},
		// A full stop is not a different value.
		{"retries are on.", "retries are on"},
		// A denial with nothing left denies nothing in particular.
		{"Never.", "retries are on"},
		// Numbers are Values' to compare.
		{"the timeout is 30", "the timeout is 60"},
	} {
		if Reverses(p[0], p[1]) {
			t.Errorf("Reverses(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

func TestFlattenCollapsesWhitespaceWithoutShortening(t *testing.T) {
	if got := Flatten("  one\n\ttwo   three \n"); got != "one two three" {
		t.Errorf("Flatten = %q", got)
	}
}
