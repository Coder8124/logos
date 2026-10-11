package memory

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// sameMeaning is an embedding runtime that hears every text as the same
// sentence, so any second memory folds into the first.
func sameMeaning(t *testing.T) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"embedding":[1,0,0]}]}`))
	}))
	t.Cleanup(srv.Close)
	return provider.New("test", srv.URL, "")
}

// A re-worded import folds into the memory it restates and is logged under its
// own words only as a reinforcement. Once the user forgets that memory, those
// words are still something Logos has seen and must not import afresh.
func TestAnImportFoldedIntoAMemoryCountsAsSeen(t *testing.T) {
	db := testDB(t)
	p := sameMeaning(t)
	if _, err := Store(db, p, "e", &Memory{Text: "Staging runs on port 9090", Kind: Fact}); err != nil {
		t.Fatal(err)
	}
	r, err := Store(db, p, "e", &Memory{Text: "Staging still runs on port 9090", Kind: Fact})
	if err != nil || r.Outcome != EvReinforced {
		t.Fatalf("the restatement was %q (%v), want it folded in", r.Outcome, err)
	}
	seen, err := SeenTexts(db)
	if err != nil {
		t.Fatal(err)
	}
	if !seen["Staging still runs on port 9090"] {
		t.Fatalf("the folded-in import does not count as seen: %v", seen)
	}
}
