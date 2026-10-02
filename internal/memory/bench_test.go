package memory

import "testing"

func TestNormalizeSessionIDMatchesConventions(t *testing.T) {
	// Evidence ids ("answer_<hash>") and haystack ids should compare equal when
	// they refer to the same session.
	if normalizeSessionID("answer_280352e9") != normalizeSessionID("280352e9") {
		t.Error("identical ids should match")
	}
	// A plain haystack id is unchanged.
	if normalizeSessionID("sharegpt_abc_0") != "sharegpt_abc_0" {
		t.Error("non-answer ids should pass through")
	}
}

func TestBenchResultRecall(t *testing.T) {
	r := BenchResult{N: 4, HitsAt: map[int]int{5: 3}}
	if r.RecallAt(5) != 0.75 {
		t.Errorf("recall@5 = %v, want 0.75", r.RecallAt(5))
	}
	if (BenchResult{}).RecallAt(5) != 0 {
		t.Error("empty result should be 0, not NaN")
	}
}
