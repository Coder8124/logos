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
