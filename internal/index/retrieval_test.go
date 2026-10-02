package index

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// embedAxes are the dimensions of the fake embedder: a text's vector has a 1
// on each axis whose word it contains. That makes cosine order predictable, so
// these tests can say which note should win and why, without a model.
var embedAxes = []string{"waveguide", "procurement", "lead", "frame"}

// fakeEmbedder serves /v1/embeddings on loopback and counts the requests.
func fakeEmbedder(t *testing.T) (*provider.Provider, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var data []map[string][]float32
		for _, in := range req.Input {
			vec := make([]float32, len(embedAxes))
			for i, axis := range embedAxes {
				if strings.Contains(strings.ToLower(in), axis) {
					vec[i] = 1
				}
			}
			data = append(data, map[string][]float32{"embedding": vec})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("Ollama", srv.URL+"/v1", ""), &calls
}

func slugs(hits []Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Slug)
	}
	return strings.Join(out, ",")
}

// A round trip per note made a 2000-note vault take minutes, so notes go in
// batches; and a note that already has a vector is not paid for again.
func TestEmbedPendingBatchesAndSkipsNotesThatAlreadyHaveAVector(t *testing.T) {
	ix := newTestIndex(t)
	for i := range 5 {
		seed(t, ix, fmt.Sprintf("n%d", i), "Note", "waveguide")
	}
	p, calls := fakeEmbedder(t)

	n, err := ix.EmbedPending(p, "m", 2)
	if err != nil || n != 5 {
		t.Fatalf("EmbedPending = %d, %v; want all 5", n, err)
	}
	if c := calls.Load(); c != 3 {
		t.Errorf("5 notes in batches of 2 took %d requests, want 3", c)
	}
	if n, err := ix.EmbedPending(p, "m", 2); err != nil || n != 0 || calls.Load() != 3 {
		t.Errorf("a second pass embedded %d notes in %d more requests; every note already had a vector", n, calls.Load()-3)
	}
}

// The count is what `logos index` reports, so a runtime that fails partway
// must leave it saying how far it got, not zero and not all.
func TestEmbedPendingReportsHowFarItGotWhenTheRuntimeFails(t *testing.T) {
	ix := newTestIndex(t)
	for i := range 4 {
		seed(t, ix, fmt.Sprintf("n%d", i), "Note", "waveguide")
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			http.Error(w, "out of memory", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"data":[{"embedding":[1,0]},{"embedding":[0,1]}]}`)
	}))
	defer srv.Close()

	n, err := ix.EmbedPending(provider.New("Ollama", srv.URL+"/v1", ""), "m", 2)
	if err == nil {
		t.Fatal("a runtime that failed the second batch was reported as success")
	}
	if n != 2 {
		t.Errorf("EmbedPending = %d, want the 2 that were stored before the failure", n)
	}
}

func TestSearchRanksByMeaningAndKeepsTheTopK(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "quote", "Quote", "the waveguide quote for procurement")
	seed(t, ix, "frame", "Frame", "the extruded frame")
	seed(t, ix, "bom", "BOM", "waveguide line items")
	p, _ := fakeEmbedder(t)
	if _, err := ix.EmbedPending(p, "m", 8); err != nil {
		t.Fatal(err)
	}

	hits, err := ix.Search(p, "m", "procurement waveguide", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := slugs(hits); got != "quote,bom" {
		t.Errorf("Search = %s, want quote (both words) then bom (one), and frame cut by k", got)
	}
}

// RRF is only worth having if a note one arm misses still comes back from the
// other, with its title, so it can be cited.
func TestHybridSearchReturnsANoteOnlyTheLexicalArmFound(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "quote", "Quote", "the waveguide quote")
	p, _ := fakeEmbedder(t)
	if _, err := ix.EmbedPending(p, "m", 8); err != nil {
		t.Fatal(err)
	}
	// Seeded after embedding, so it has no vector: only FTS can find it.
	seed(t, ix, "sku", "SKU list", "part 4471 is the waveguide")

	hits, err := ix.HybridSearch(p, "m", "4471", 5)
	if err != nil {
		t.Fatal(err)
	}
	var found *Hit
	for i := range hits {
		if hits[i].Slug == "sku" {
			found = &hits[i]
		}
	}
	if found == nil {
		t.Fatalf("HybridSearch = %s; the exact part number matched no vector and was lost", slugs(hits))
	}
	if found.Title != "SKU list" {
		t.Errorf("the lexical-only hit came back with title %q, so it cannot be cited", found.Title)
	}
}

// Asking about a project should surface the people on it even when their
// notes share no words with the question — but each once, and only along
// links the extractor was confident in.
func TestExpandFollowsConfidentLinksOnceAndSaysWhy(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "kestrel", "Kestrel", "the project")
	seed(t, ix, "people/ana", "Ana", "buyer")
	seed(t, ix, "people/raj", "Raj", "engineer")
	for _, e := range []struct {
		obj  string
		conf float64
	}{{"ana", 0.9}, {"people/ana", 0.9}, {"raj", 0.3}} {
		if _, err := ix.DB.Exec("INSERT INTO edges (src_slug, pred, obj, conf, src) VALUES ('kestrel', 'owned_by', ?, ?, 'test')",
			e.obj, e.conf); err != nil {
			t.Fatal(err)
		}
	}

	out, err := ix.Expand([]Hit{{Slug: "kestrel", Title: "Kestrel", Score: 1}}, 0.6, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := slugs(out); got != "people/ana" {
		t.Fatalf("Expand = %s, want people/ana once (two edges reach her) and not raj (conf 0.3)", got)
	}
	if out[0].Via != "Kestrel —owned_by→" || out[0].Score != 0.5 {
		t.Errorf("neighbour = via %q score %.2f, want it to say which hit pulled it in, at half that hit's score", out[0].Via, out[0].Score)
	}

	if out, _ := ix.Expand([]Hit{{Slug: "kestrel"}, {Slug: "people/ana"}}, 0.6, 5); len(out) != 0 {
		t.Errorf("Expand returned %s, a note that was already a hit", slugs(out))
	}
}

func TestNoteAndEdgeCountsCountRows(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "a", "A", "x")
	seed(t, ix, "b", "B", "y")
	if _, err := ix.DB.Exec("INSERT INTO edges (src_slug, pred, obj, conf, src) VALUES ('a', 'links', 'b', 1, 'test')"); err != nil {
		t.Fatal(err)
	}
	if n, err := ix.NoteCount(); err != nil || n != 2 {
		t.Errorf("NoteCount = %d, %v", n, err)
	}
	if n, err := ix.EdgeCount(); err != nil || n != 1 {
		t.Errorf("EdgeCount = %d, %v", n, err)
	}
}
