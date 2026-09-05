package rollup

import (
	"testing"

	"github.com/Coder8124/brain/internal/routine"
)

// ProposeRoutines' own doc comment promises naming can never block a
// proposal: no model just means a plainer description. A nil router — the
// caller could not find a local runtime — is exactly that case, and must
// degrade the same way, not panic before a single proposal is queued.
func TestProposeRoutinesWithNilRouterStillQueuesPlainDescriptions(t *testing.T) {
	db := testDB(t)
	periodics := []routine.Periodic{{
		App: "editor", Weekday: true, MedianStart: 9 * 3600, SpreadS: 600,
		Occurrences: 12, Weeks: 4, Consistency: 0.8, EventIDs: []int64{1, 2, 3},
	}}

	n, err := ProposeRoutines(db, nil, periodics, nil)
	if err != nil {
		t.Fatalf("proposing routines with no model runtime must not error, got: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 routine queued with a plain description, got %d", n)
	}
}
