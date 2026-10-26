package usage

import "testing"

// Tallies counts only what was said about procedures: a dead-end check's refs
// are not showings, whatever they fingerprint.
func TestTalliesCountShowingsAndProvenSessionsByProcedure(t *testing.T) {
	a, b := Ref("procedure a"), Ref("procedure b")
	got := Tallies([]Event{
		{Kind: KindProcedure, Refs: []string{a, b}},
		{Kind: KindProcedure, Refs: []string{a}},
		{Kind: KindProven, Refs: []string{a}},
		{Kind: KindDeadEnd, Refs: []string{b}},
	})
	if got[a] != (Tally{Shown: 2, Proven: 1}) || got[b] != (Tally{Shown: 1}) {
		t.Errorf("tallies = %+v", got)
	}
}

// The stats totals left procedures out, so a project whose ledger held only
// before_you_try procedure lines read as one where nothing was recorded.
func TestTheTotalsCountProceduresHandedBackAndProven(t *testing.T) {
	a, b := Ref("procedure a"), Ref("procedure b")
	got := Sum([]Event{
		{Kind: KindProcedure, Refs: []string{a, b}},
		{Kind: KindProven, Refs: []string{a}},
	}, "")
	if got.Procedures != 2 || got.Proven != 1 {
		t.Errorf("totals = %+v, want 2 procedures and 1 proven", got)
	}
}
