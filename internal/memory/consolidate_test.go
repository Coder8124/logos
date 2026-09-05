package memory

import (
	"errors"
	"testing"

	"github.com/Coder8124/brain/internal/router"
)

// A nil router is exactly the "no local model runtime" case dream.nrem is
// built to skip, not fail on — but Consolidate called rt.ModelFor directly
// with no guard, so a caller that had already resolved this down to a nil
// *router.Router got a nil-pointer panic instead of the ErrNoRuntime its own
// caller's error-handling switch was written to expect.
func TestConsolidateWithNilRouterReturnsErrNoRuntimeInsteadOfPanicking(t *testing.T) {
	db := testDB(t)

	_, _, err := Consolidate(db, nil)
	if !errors.Is(err, router.ErrNoRuntime) {
		t.Fatalf("Consolidate(db, nil) = %v, want an error wrapping router.ErrNoRuntime", err)
	}
}
