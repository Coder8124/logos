package eval

import (
	"reflect"
	"strings"
	"testing"
)

// A rewording that contains a gold term passes on its own echo whenever the
// response paraphrases the question, and stripEcho only removes the verbatim
// task — so the variant would measure the wording, not the system.
func TestEveryScenarioHasThreeRewordingsThatDoNotGiveAwayTheAnswer(t *testing.T) {
	for _, sc := range Suite() {
		if len(sc.Wordings) < 3 {
			t.Errorf("%s has %d rewordings, want at least 3", sc.ID, len(sc.Wordings))
		}
		terms := goldTerms(sc.Gold)
		seen := map[string]bool{normalize(sc.Query.Task): true}
		for _, w := range sc.Wordings {
			if seen[normalize(w)] {
				t.Errorf("%s: rewording %q repeats the task or another rewording", sc.ID, w)
			}
			seen[normalize(w)] = true
			for _, term := range terms {
				if strings.Contains(normalize(w), normalize(term)) {
					t.Errorf("%s: rewording %q contains the gold term %q", sc.ID, w, term)
				}
			}
		}
	}
}

// A distractor that carried a gold term would make a case pass or leak by
// noise alone. The pool is filtered per case; this holds the filter to it.
func TestInjectedDistractorsNeverCarryTheAnswer(t *testing.T) {
	base := map[string]Scenario{}
	for _, sc := range Suite() {
		base[sc.ID] = sc
	}
	for _, v := range Expand(Suite(), 2) {
		if v.Axis != AxisDistractors {
			continue
		}
		added := v.Setup[len(base[v.Base].Setup):]
		if len(added) == 0 {
			t.Errorf("%s added no history, so it tests nothing the case as written does not", v.ID)
		}
		for _, e := range added {
			if containsAny(e.Text, goldTerms(v.Gold)) {
				t.Errorf("%s: distractor %q contains a gold term", v.ID, e.Text)
			}
		}
	}
}

// A variant that changed between runs would put back the run-to-run noise the
// variants exist to measure, and two runs could no longer be compared.
func TestDistractorVariantsAreTheSameOnEveryRun(t *testing.T) {
	a, b := Expand(Suite(), 2), Expand(Suite(), 2)
	for i := range a {
		if !reflect.DeepEqual(a[i].Setup, b[i].Setup) {
			t.Fatalf("%s drew different distractors on a second expansion", a[i].ID)
		}
	}
	d1, d2 := distractors(Suite()[0], 1), distractors(Suite()[0], 2)
	if reflect.DeepEqual(d1, d2) {
		t.Error("two seeds drew the same distractor set, so the second adds nothing")
	}
}

func TestAnEmptyAnswerScoresNothingOnAnyVariant(t *testing.T) {
	scores, err := Run(silent{}, Expand(Suite(), 2), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scores {
		if s.Pass() {
			t.Errorf("returning nothing passed %s", s.Scenario)
		}
	}
}

func scored(base, axis, variant string, pass bool) Score {
	s := Score{Scenario: base, Base: base, Axis: axis, Variant: variant, Family: "memory", Skill: "recall", CarryTotal: 1}
	if axis != "" {
		s.Scenario = base + "~" + axis
	}
	if pass {
		s.CarryHit = 1
	}
	return s
}

// The headline has to stay comparable with every run made before variants
// existed; averaging in the variants would move it without the system changing.
func TestTheHeadlineStaysOnTheScenariosAsWritten(t *testing.T) {
	r := []Result{{Adapter: "logos", Scores: []Score{
		scored("a", "", "", true),
		scored("a", AxisWording, `"reworded"`, false),
		scored("a", AxisDistractors, "distractor set 1", false),
	}}}
	out := Report(r, false)
	overall := out[strings.Index(out, "── overall"):strings.Index(out, "pass = ")]
	if !strings.Contains(overall, "100.0%") {
		t.Errorf("the overall table did not report the case as written:\n%s", overall)
	}
}

// The point of the variants is the scenario that passes one way and fails
// another; a rate alone would hide which one and how it was asked.
func TestAFlakyScenarioIsNamedWithTheVariantsItFailed(t *testing.T) {
	r := []Result{{Adapter: "logos", Scores: []Score{
		scored("steady", "", "", true),
		scored("steady", AxisWording, `"x"`, true),
		scored("flaky", "", "", true),
		scored("flaky", AxisWording, `"where did we leave off?"`, false),
		scored("flaky", AxisDistractors, "distractor set 2", true),
	}}}
	out := Report(r, false)
	if !strings.Contains(out, "flaky") || !strings.Contains(out, "2/3") ||
		!strings.Contains(out, `reworded "where did we leave off?"`) {
		t.Errorf("the flaky case was not named with its failing variant:\n%s", out)
	}
	if strings.Contains(out[strings.Index(out, "── across variants"):], "steady ") {
		t.Errorf("a case with one outcome on every variant was listed as flaky:\n%s", out)
	}
}
