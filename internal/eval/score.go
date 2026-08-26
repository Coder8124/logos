package eval

import (
	"sort"
	"strings"
)

// Scoring is deliberately mechanical.
//
// Every headline number here can be computed with no model running, which is
// what makes the suite reproducible on a plane and comparable between two
// people who never talk to each other. A model-judged layer exists (judge.go)
// and answers a question matching cannot — whether an agent handed this context
// actually avoids repeating the ruled-out work — but it sits on top and is
// reported separately. If the two ever disagree, that disagreement is a finding
// about the metric, not a tie to be broken silently.

// A Fact is something the harness looks for in a response. Matching is
// substring, case-insensitive, over whitespace-collapsed text.
//
// Surface variants are the scenario author's job, not the matcher's: write
// Any: {"71 percent", "71%"} rather than hoping a cleverer matcher guesses. A
// fuzzy matcher that silently accepts near-misses is how a benchmark starts
// flattering everyone equally.
type Fact struct {
	Label string   // what to call this in the report
	Any   []string // at least one must appear
	All   []string // every one must appear (for facts that are only right together)
}

// In reports whether the fact is present in the given text.
func (f Fact) In(hay string) bool {
	hay = normalize(hay)
	if len(f.All) > 0 {
		for _, want := range f.All {
			if !strings.Contains(hay, normalize(want)) {
				return false
			}
		}
	}
	if len(f.Any) > 0 {
		for _, want := range f.Any {
			if strings.Contains(hay, normalize(want)) {
				return true
			}
		}
		return false
	}
	return len(f.All) > 0
}

func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Gold is what a correct answer looks like.
type Gold struct {
	// Carry must appear. These are the facts the arriving agent cannot do the
	// job without.
	Carry []Fact
	// Avoid must NOT appear — a superseded price, a cancelled plan, a value the
	// user corrected. Carrying a stale fact forward is worse than carrying
	// nothing, because the agent will act on it.
	Avoid []Fact
	// Signal is meta-knowledge about the answer rather than the answer: that the
	// information is old, that the store does not know, that a finding came from
	// a particular agent. Scored separately because almost every system alive
	// scores zero here, and folding that into the main number would say more
	// about the state of the art than about any one system.
	Signal []Fact
}

// Known is what brain is expected to do on a case today. Recording the
// prediction in the suite is what turns a weakness into a tracked one: a
// "weakness" that starts passing is progress worth noticing, and a "strength"
// that starts failing is a regression the aggregate numbers would otherwise
// average away.
type Known string

const (
	KnownStrength Known = "strength"
	KnownWeakness Known = "weakness"
)

// A Scenario is one case: a history, a task, and what a correct answer carries.
type Scenario struct {
	ID     string
	Family string // continuity | memory | durability
	Skill  string // the specific capability under test
	Why    string // one line, for the report — what this case is really asking
	Known  Known  // brain's expected outcome, so regressions are visible

	Setup []Event
	Query Query
	Gold  Gold

	// DropDerived wipes every rebuildable artifact between writing and reading.
	// Systems that keep no source of truth outside their own index return
	// nothing after this, which is the intended result.
	DropDerived bool
}

// A Score is one adapter's result on one scenario.
type Score struct {
	Scenario string
	Family   string
	Skill    string
	Known    Known
	Adapter  string

	CarryHit, CarryTotal   int
	LeakHit, LeakTotal     int
	SignalHit, SignalTotal int

	Tokens int
	Budget int

	Missed      []string
	Leaked      []string
	Unsignalled []string
	Err         error
}

func ratio(hit, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

// Recall is the share of required facts that survived.
//
// A scenario that requires nothing scores 1, not 0. The abstention cases have
// no Carry labels by design — the correct answer is an admission of ignorance,
// and there is no fact to fetch. Dividing zero by zero and calling it a miss
// marked every system down for declining to invent one.
func (s Score) Recall() float64 {
	if s.CarryTotal == 0 {
		return 1
	}
	return ratio(s.CarryHit, s.CarryTotal)
}

// Leakage is the share of facts that should have been suppressed but were not.
func (s Score) Leakage() float64 { return ratio(s.LeakHit, s.LeakTotal) }

// Signal is the share of meta-properties the answer exhibited.
func (s Score) Signal() float64 { return ratio(s.SignalHit, s.SignalTotal) }

// Fidelity is the headline: carrying what is needed while not carrying what is
// wrong. Multiplicative rather than averaged, because a response that carries
// every required fact alongside a superseded price is not "half right" — the
// agent will act on the stale number, and the correct facts sitting next to it
// do not undo that.
func (s Score) Fidelity() float64 { return s.Recall() * (1 - s.Leakage()) }

// Pass is whether the scenario was satisfied on every axis it declares.
//
// Fidelity alone is not enough, and the staleness case is why: its gold labels
// ask for the note (carried) and for its age (a signal). Judged on fidelity it
// scored 100% while doing precisely the thing the case was written to catch. A
// scenario passes when it meets every bar it set, not the most flattering one.
func (s Score) Pass() bool {
	const bar = 0.75
	if s.Err != nil || s.Fidelity() < bar {
		return false
	}
	return s.SignalTotal == 0 || s.Signal() >= bar
}

// Density is required facts carried per 1000 tokens spent. The axis on which
// "put the whole history in the window" loses: it scores well on recall by
// brute force and terribly here, and the window it burns is the window the
// actual conversation needed.
func (s Score) Density() float64 {
	if s.Tokens == 0 {
		return 0
	}
	return float64(s.CarryHit) * 1000 / float64(s.Tokens)
}

// Over reports whether the response blew the token ceiling it was given.
func (s Score) Over() bool { return s.Budget > 0 && s.Tokens > s.Budget }

// stripEcho removes the question from the answer before matching.
//
// Systems that head their output with the task they were given ("Context for:
// did we lock the design before or after signing with Pegatron?") would
// otherwise satisfy any gold label whose wording overlaps the question. The
// temporal-ordering case scored a clean 100% this way while returning two
// undated facts in arbitrary order — it matched "after signing" in the echoed
// header. Only the verbatim query string is removed, so a real note that
// happens to share words with the question still counts.
func stripEcho(text string, q Query) string {
	hay := normalize(text)
	for _, echo := range []string{q.Task, q.Project} {
		if e := normalize(echo); len(e) > 8 {
			hay = strings.ReplaceAll(hay, e, " ")
		}
	}
	return hay
}

// grade scores one response against a scenario's gold labels.
func grade(sc Scenario, ad string, r Response) Score {
	out := Score{
		Scenario: sc.ID, Family: sc.Family, Skill: sc.Skill, Known: sc.Known,
		Adapter: ad, Tokens: Tokens(r.Text), Budget: sc.Query.Budget, Err: r.Err,
	}
	hay := stripEcho(r.Text, sc.Query)

	for _, f := range sc.Gold.Carry {
		out.CarryTotal++
		if f.In(hay) {
			out.CarryHit++
		} else {
			out.Missed = append(out.Missed, f.Label)
		}
	}
	for _, f := range sc.Gold.Avoid {
		out.LeakTotal++
		if f.In(hay) {
			out.LeakHit++
			out.Leaked = append(out.Leaked, f.Label)
		}
	}
	for _, f := range sc.Gold.Signal {
		out.SignalTotal++
		if f.In(hay) {
			out.SignalHit++
		} else {
			out.Unsignalled = append(out.Unsignalled, f.Label)
		}
	}
	return out
}

// An Aggregate rolls scores up for one adapter over one slice of the suite.
type Aggregate struct {
	Adapter string
	Group   string
	N       int

	Recall   float64
	Leakage  float64
	Signal   float64
	Fidelity float64
	Density  float64
	// PassRate is the share of scenarios that met every bar they set. It is the
	// honest summary for families where fidelity does not apply — an abstention
	// case has no facts to carry, so it scores full recall by definition and
	// only the signal says whether the system did the right thing.
	PassRate float64
	Tokens   int // mean
	OverRuns int // responses that exceeded their budget
	Errors   int
}

// Roll aggregates scores by a grouping function. Means are unweighted across
// scenarios: every case counts once, so a family with more facts in its gold
// labels does not quietly dominate the headline.
func Roll(scores []Score, group func(Score) string) []Aggregate {
	byGroup := map[string][]Score{}
	var order []string
	for _, s := range scores {
		g := group(s)
		if _, seen := byGroup[g]; !seen {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], s)
	}
	sort.Strings(order)

	out := make([]Aggregate, 0, len(order))
	for _, g := range order {
		ss := byGroup[g]
		a := Aggregate{Group: g, N: len(ss)}
		if len(ss) > 0 {
			a.Adapter = ss[0].Adapter
		}
		var tokens int
		// Leakage and Signal are averaged only over the scenarios that actually
		// test them. Counting a case with no Avoid labels as perfect leakage
		// would let a suite dilute the metric just by adding unrelated cases.
		var leakN, sigN int
		for _, s := range ss {
			a.Recall += s.Recall()
			a.Fidelity += s.Fidelity()
			a.Density += s.Density()
			if s.Pass() {
				a.PassRate++
			}
			tokens += s.Tokens
			if s.LeakTotal > 0 {
				a.Leakage += s.Leakage()
				leakN++
			}
			if s.SignalTotal > 0 {
				a.Signal += s.Signal()
				sigN++
			}
			if s.Over() {
				a.OverRuns++
			}
			if s.Err != nil {
				a.Errors++
			}
		}
		n := float64(len(ss))
		a.Recall /= n
		a.Fidelity /= n
		a.Density /= n
		a.PassRate /= n
		a.Tokens = tokens / len(ss)
		if leakN > 0 {
			a.Leakage /= float64(leakN)
		}
		if sigN > 0 {
			a.Signal /= float64(sigN)
		}
		out = append(out, a)
	}
	return out
}
