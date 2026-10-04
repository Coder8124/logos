package memory

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Coder8124/logos/internal/router"
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

// chatRuntime is a configured runtime that lists the default T1 model and
// answers every chat with the same JSON: ok for the router's capability probe,
// relation for Consolidate's classifier.
func chatRuntime(t *testing.T, relation string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Write([]byte(`{"data":[{"id":"gemma3:4b"}]}`))
		case "/chat/completions":
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"{\"ok\":true,\"relation\":\"%s\"}"}}]}`, relation)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LOGOS_RUNTIME", srv.URL)
}

// Consolidate counted a merge or a supersession whether or not the UPDATE
// that performed it went through, and never looked at the error. With the
// database refusing writes, `logos memory consolidate` reported "merged 1"
// over a store it had not changed — a failure returned in the shape of a
// success, which is the one outcome this codebase does not allow.
func TestAConsolidationTheDatabaseRefusedIsReportedAsAnErrorNotAsAMerge(t *testing.T) {
	for _, relation := range []string{"duplicate", "update"} {
		t.Run(relation, func(t *testing.T) {
			chatRuntime(t, relation)
			db := testDB(t)
			storeVec(t, db, "deploys go out on Tuesdays", Fact, 0.5, []float32{1, 0, 0})
			storeVec(t, db, "deploys go out on Tuesday", Fact, 0.5, []float32{1, 0, 0})
			if _, err := db.Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON memories BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`); err != nil {
				t.Fatal(err)
			}
			rt, err := router.New(nil, "")
			if err != nil {
				t.Fatal(err)
			}

			merged, superseded, err := Consolidate(db, rt)
			if err == nil {
				t.Error("Consolidate returned no error although every UPDATE was refused")
			}
			if merged+superseded != 0 {
				t.Errorf("Consolidate reported merged=%d superseded=%d although nothing was written", merged, superseded)
			}
		})
	}
}
