package eval

import (
	"strings"
	"testing"
)

// The harness scores other people's systems, so its own failure modes are
// expensive: a matcher that is too generous inflates everyone equally and the
// benchmark stops distinguishing anything. These tests are mostly about the
// ways matching can be wrong in a system's favour.

// The bug that made this test necessary: brain's render opens with "Context
// for: <the task>", so a gold label whose wording overlapped the question was
// satisfied by the echo. temporal-ordering scored 100% while returning two
// undated facts in arbitrary order.
func TestTheQuestionCannotAnswerItself(t *testing.T) {
	q := Query{Task: "did we lock the industrial design before or after signing with Pegatron?"}
	echoed := "# Context for: did we lock the industrial design before or after signing with Pegatron?\n" +
		"- Signed the Pegatron agreement.\n- Locked the industrial design.\n"

	f := Fact{Label: "ordering", Any: []string{"after signing", "signed first"}}
	if f.In(stripEcho(echoed, q)) {
		t.Error("a label matched text the system copied from the question")
	}

	// The same words in real content still count — only the verbatim query goes.
	real := echoed + "The design was locked after signing with the manufacturer.\n"
	if !f.In(stripEcho(real, q)) {
		t.Error("stripping the echo also removed a genuine answer")
	}
}

// Abstention cases have no facts to fetch: the right answer is an admission of
// ignorance. Scoring them as a total recall miss marked every system down for
// declining to invent something.
func TestRequiringNothingIsNotAMiss(t *testing.T) {
	sc := Scenario{
		ID: "t", Query: Query{Task: "what is the warranty period?"},
		Gold: Gold{Signal: []Fact{{Label: "admits ignorance", Any: []string{"no record"}}}},
	}

	honest := grade(sc, "x", Response{Text: "No record of a warranty period."})
	if honest.Recall() != 1 {
		t.Errorf("nothing required, nothing missed: want recall 1, got %v", honest.Recall())
	}
	if !honest.Pass() {
		t.Error("admitting ignorance is the correct answer here and must pass")
	}

	silent := grade(sc, "x", Response{Text: "The Suzhou plant runs two shifts."})
	if silent.Pass() {
		t.Error("changing the subject is not an admission of ignorance")
	}
}

// Fidelity is multiplicative: carrying a superseded price next to the right one
// is not half credit, because the agent will act on the stale number.
func TestALeakedFactCancelsTheCorrectOne(t *testing.T) {
	sc := Scenario{
		ID: "t", Query: Query{Task: "price?"},
		Gold: Gold{
			Carry: []Fact{{Label: "current", Any: []string{"$249"}}},
			Avoid: []Fact{{Label: "superseded", Any: []string{"$199"}}},
		},
	}
	clean := grade(sc, "x", Response{Text: "Retail is $249."})
	leaky := grade(sc, "x", Response{Text: "Retail is $249. Earlier we said $199."})

	if clean.Fidelity() != 1 {
		t.Errorf("want 1, got %v", clean.Fidelity())
	}
	if leaky.Fidelity() != 0 {
		t.Errorf("a leaked superseded value must not score: got %v", leaky.Fidelity())
	}
	if leaky.Recall() != 1 {
		t.Error("recall should still record that the right fact was present")
	}
}

// A scenario passes only when it meets every bar it set. Fidelity alone let the
// staleness case score 100% while doing exactly what it was written to catch.
func TestSignalCountsTowardPassing(t *testing.T) {
	sc := Scenario{
		ID: "t", Query: Query{Task: "plan the next spin"},
		Gold: Gold{
			Carry:  []Fact{{Label: "the note", Any: []string{"tooling freeze"}}},
			Signal: []Fact{{Label: "its age", Any: []string{"days ago"}}},
		},
	}
	undated := grade(sc, "x", Response{Text: "Sixteen days before the tooling freeze."})
	if undated.Fidelity() != 1 {
		t.Errorf("fidelity should be clean here: got %v", undated.Fidelity())
	}
	if undated.Pass() {
		t.Error("the case asked for the age of the note and did not get it")
	}

	dated := grade(sc, "x", Response{Text: "Sixteen days before the tooling freeze. (13 days ago)"})
	if !dated.Pass() {
		t.Error("both bars were met")
	}
}

// All means all; Any means at least one. Compound facts exist because some
// claims are only right together.
func TestFactMatching(t *testing.T) {
	both := Fact{All: []string{"claude", "cursor"}}
	if both.In("only claude was here") {
		t.Error("All must require every term")
	}
	if !both.In("claude found it, cursor confirmed it") {
		t.Error("All should match when every term is present")
	}

	either := Fact{Any: []string{"71 percent", "71%"}}
	if !either.In("yield runs at 71% first-pass") {
		t.Error("Any should accept a surface variant the author listed")
	}
	if either.In("yield runs at seventy-one percent") {
		t.Error("Any must not match a variant the author did not list — that is the author's job")
	}
}

// Whitespace and case are noise; a system should not be punished for wrapping.
func TestMatchingIgnoresLayout(t *testing.T) {
	f := Fact{Any: []string{"no movement under 10k units"}}
	if !f.In("Re-quoting the waveguide —\n  No Movement Under\n  10k Units.") {
		t.Error("matching should survive wrapping and capitalisation")
	}
}

// Leakage and signal are averaged only over the cases that test them, so a
// suite cannot dilute either metric by adding unrelated scenarios.
func TestUntestedMetricsDoNotDilute(t *testing.T) {
	scores := []Score{
		{CarryHit: 1, CarryTotal: 1, LeakHit: 1, LeakTotal: 1}, // leaks everything
		{CarryHit: 1, CarryTotal: 1},                           // tests no leakage at all
	}
	got := Roll(scores, func(Score) string { return "all" })[0]
	if got.Leakage != 1 {
		t.Errorf("want leakage 1 over the single case that tests it, got %v", got.Leakage)
	}
}

// silent answers nothing. Defined here rather than imported from the adapters
// package, which depends on this one.
type silent struct{}

func (silent) Name() string                 { return "silent" }
func (silent) Reset() error                 { return nil }
func (silent) Write(Event) error            { return nil }
func (silent) Read(Query) (Response, error) { return Response{}, nil }
func (silent) Close() error                 { return nil }

// The floor has to be a floor. If doing nothing scores, the suite is measuring
// nothing — this is the guard that keeps scenario authors honest.
func TestAnEmptyAnswerScoresNothing(t *testing.T) {
	suite := Suite()
	scores, err := Run(silent{}, suite, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var passed []string
	for _, s := range scores {
		if s.Pass() {
			passed = append(passed, s.Scenario)
		}
	}
	if len(passed) > 0 {
		t.Errorf("returning nothing passed %d scenarios: %s", len(passed), strings.Join(passed, ", "))
	}
}

// Every scenario must be able to fail and must say what it is for, or it is
// decoration rather than measurement.
func TestSuiteIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Suite() {
		if seen[s.ID] {
			t.Errorf("duplicate scenario id %q", s.ID)
		}
		seen[s.ID] = true

		if s.Why == "" || s.Family == "" || s.Skill == "" {
			t.Errorf("%s: needs a family, a skill, and a line saying what it tests", s.ID)
		}
		if len(s.Gold.Carry)+len(s.Gold.Avoid)+len(s.Gold.Signal) == 0 {
			t.Errorf("%s: no gold labels, so nothing can fail", s.ID)
		}
		if len(s.Setup) == 0 {
			t.Errorf("%s: no history to retrieve from", s.ID)
		}
		if s.Query.Task == "" {
			t.Errorf("%s: no task", s.ID)
		}
	}
}

// A checkpoint has to survive being flattened, because that is the only form
// systems without a checkpoint primitive can receive. If Flatten dropped the
// ruled-out approaches, every comparison in the suite would be rigged.
func TestFlatteningAChveckpointKeepsWhatFailed(t *testing.T) {
	got := Event{
		Kind: KindCheckpoint, Actor: "claude", Project: "kestrel-one",
		Task:   "cut the BOM",
		Failed: []string{"re-quoting the waveguide", "dropping the second mic"},
		Next:   "quote the display driver",
	}.Flatten()

	for _, want := range []string{"cut the BOM", "re-quoting the waveguide",
		"dropping the second mic", "quote the display driver", "claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("flattening lost %q:\n%s", want, got)
		}
	}
}
