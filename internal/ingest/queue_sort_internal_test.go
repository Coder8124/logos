package ingest

import "testing"

// Harvested is 0 for a candidate written before the field existed or one whose
// frontmatter would not parse. Sorting purely on Harvested then left equal-0
// candidates in walk order, so the same queue printed differently run to run.
// The tie must break on Started and then the stable filename.
func TestSessionsWithNoEndTimeStillSortStably(t *testing.T) {
	in := []Candidate{
		{Harness: "codex", SessionID: "ccc", Harvested: 0, Started: 0},
		{Harness: "codex", SessionID: "aaa", Harvested: 0, Started: 0},
		{Harness: "codex", SessionID: "bbb", Harvested: 0, Started: 100},
	}
	want := []string{"bbb", "aaa", "ccc"} // Started 100 first, then filename order

	got1 := append([]Candidate(nil), in...)
	sortCandidatesNewestFirst(got1)

	// A different starting permutation must land on the same order.
	got2 := []Candidate{in[2], in[0], in[1]}
	sortCandidatesNewestFirst(got2)

	for i := range want {
		if got1[i].SessionID != want[i] || got2[i].SessionID != want[i] {
			t.Fatalf("unstable sort: got1=%v got2=%v want=%v",
				ids(got1), ids(got2), want)
		}
	}
}

func ids(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.SessionID
	}
	return out
}
