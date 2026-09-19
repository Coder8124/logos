package memory

import "testing"

// A model picks its own tool arguments, and `limit: -1` for "no limit" is a
// common guess. It reached `mems[:k]` and panicked — and because the MCP
// server handles each request on its own goroutine with no recover, the whole
// process exited: every Logos tool gone for the rest of the host's session,
// and the main goroutine's defers, which record the session's unsaved work,
// never ran.
func TestANegativeRecallLimitDoesNotPanic(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the billing service retries twice", Context, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "Sarah owns billing", Person, 0.5, []float32{1, 0, 0})

	got, err := recallScoped(db, []float32{1, 0, 0}, -1, "billing", "", "", false)
	if err != nil {
		t.Fatalf("a negative limit errored instead of being clamped: %v", err)
	}
	if len(got) == 0 {
		t.Error("a negative limit returned nothing while matching memories existed")
	}
}

// `limit: 0` did not panic, which made it the worse of the two: it returned an
// empty list, and the agent reads an empty list as "this project has recorded
// nothing about that". A failure shaped like a result.
func TestARecallLimitOfZeroDoesNotClaimNothingIsKnown(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the billing service retries twice", Context, 0.5, []float32{1, 0, 0})

	got, err := recallScoped(db, []float32{1, 0, 0}, 0, "billing", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("limit 0 answered that nothing is known while a matching memory existed")
	}
}
