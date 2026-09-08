package index

import (
	"testing"
)

// A hybrid search test that exercises the lexical arm and RRF fusion without a
// live embedding provider: we seed notes + FTS directly and check that an exact
// token match surfaces even with no vectors present.
func newTestIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func seed(t *testing.T, ix *Index, slug, title, body string) {
	t.Helper()
	if _, err := ix.DB.Exec(
		"INSERT INTO notes (slug, path, title, kind, body, hash) VALUES (?,?,?,?,?,?)",
		slug, slug+".md", title, "note", body, "h"+slug); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.DB.Exec("INSERT INTO notes_fts (slug, title, body) VALUES (?,?,?)",
		slug, title, body); err != nil {
		t.Fatal(err)
	}
}

func TestLexicalFindsExactToken(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "errors/e1234", "Error E1234", "the deploy failed with code E1234 on the api box")
	seed(t, ix, "topics/deploy", "Deploys", "how continuous delivery works in general")

	got, err := ix.lexical("E1234", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != "errors/e1234" {
		t.Errorf("exact token search should surface the note with that code, got %v", got)
	}
}

func TestFtsQueryStripsPunctuation(t *testing.T) {
	// A query full of FTS syntax characters must not error — it should reduce to
	// a safe OR of the real tokens.
	q := ftsQuery(`what is "eigenvalue" (ch. 4)? NEAR/2`)
	if q == "" {
		t.Fatal("expected some tokens")
	}
	// Should contain the real words, quoted.
	if !contains(q, `"eigenvalue"`) || !contains(q, `"what"`) {
		t.Errorf("unexpected fts query: %q", q)
	}
}

func TestLexicalOnGarbageQueryDoesNotError(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "a", "A", "hello world")
	if _, err := ix.lexical(`"""((()))`, 5); err != nil {
		t.Errorf("garbage query should degrade quietly, got %v", err)
	}
}

// The state BRAIN_EMBED=off puts a shell in: a runtime may be reachable, but
// the caller has asked for no embeddings. HybridSearch must degrade to the
// lexical arm rather than pass an empty (or "off") model name down to the
// runtime — which is how `brain ask` came to return a 404 from Ollama instead
// of an answer.
func TestHybridSearchWithoutAnEmbedderFallsBackToLexical(t *testing.T) {
	ix := newTestIndex(t)
	seed(t, ix, "errors/e1234", "Error E1234", "the deploy failed with code E1234 on the api box")
	seed(t, ix, "topics/deploy", "Deploys", "how continuous delivery works in general")

	got, err := ix.HybridSearch(nil, "", "E1234", 5)
	if err != nil {
		t.Fatalf("no embedder is a degrade, not an error: %v", err)
	}
	if len(got) == 0 || got[0].Slug != "errors/e1234" {
		t.Errorf("want the exact-token note surfaced lexically, got %v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
