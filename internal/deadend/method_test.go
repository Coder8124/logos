package deadend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// meaningRuntime is a local runtime that embeds by topic, not by spelling:
// anything about the frame's material lands on one axis, everything else on
// another. That is the property a real model has and the lexical arm lacks.
func meaningRuntime(t *testing.T) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i, s := range req.Input {
			v := []float32{0, 1, 0}
			if l := strings.ToLower(s); strings.Contains(l, "plastic") || strings.Contains(l, "polymer") {
				v = []float32{1, 0, 0}
			}
			data[i] = map[string]any{"embedding": v}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("Fake", srv.URL, "")
}

// The failure the check exists to prevent: the same idea in other words. No
// word of the proposal is in the ruling, so only meaning can catch it.
func TestAParaphrasedRuledOutApproachIsCaughtWithALocalModelConfigured(t *testing.T) {
	dir, db := seed(t)
	asked := "build the chassis out of polymer instead of aluminium"

	without, err := Check(dir, db, nil, "", asked, "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(without) != 0 {
		t.Fatalf("the paraphrase matched on wording alone, so this test proves nothing: %+v", without)
	}

	hits, semantic, err := CheckNoting(dir, db, meaningRuntime(t), "m", asked, "kestrel-one", 5)
	if err != nil || semantic != nil {
		t.Fatalf("err %v, semantic %v", err, semantic)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Text, "plastic frame") {
		t.Fatalf("the paraphrase was not caught: %+v", hits)
	}
	if hits[0].Method != MatchedSemantically {
		t.Errorf("method %q, want %q", hits[0].Method, MatchedSemantically)
	}
}

// With no embedder the check is lexical, which is weaker: a paraphrase walks
// straight through. Saying nothing made that weaker check read exactly like a
// full one that found nothing.
func TestWithNoLocalModelMatchingFallsBackToLexicalAndSaysSo(t *testing.T) {
	dir, db := seed(t)

	hits, semantic, err := CheckNoting(dir, db, nil, "", "switch to a plastic frame to save weight", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the lexical fallback found nothing")
	}
	note := SemanticSkipped(semantic)
	if !strings.Contains(note, "no local embedding model") || !strings.Contains(note, "wording") {
		t.Errorf("the lexical-only check was not said: %q", note)
	}
}

// A match is a claim about method. Which arm caught a ruling is part of how
// far to trust it, so the result names it rather than leaving the reader to
// guess whether "tried" meant the same words or the same idea.
func TestTheMatchResultNamesWhichMethodMatched(t *testing.T) {
	dir, db := seed(t)

	hits, _, err := CheckNoting(dir, db, nil, "", "switch to a plastic frame to save weight", "kestrel-one", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits %v, err %v", hits, err)
	}
	if hits[0].Method != MatchedLexically {
		t.Errorf("method %q, want %q", hits[0].Method, MatchedLexically)
	}
	if out := Render("switch to a plastic frame", hits); !strings.Contains(out, "matched lexically") {
		t.Errorf("the method is not in the rendered result:\n%s", out)
	}

	both, _, err := CheckNoting(dir, db, meaningRuntime(t), "m", "switch to a plastic frame to save weight", "kestrel-one", 5)
	if err != nil || len(both) == 0 {
		t.Fatalf("hits %v, err %v", both, err)
	}
	if both[0].Method != MatchedBoth {
		t.Errorf("method %q, want %q", both[0].Method, MatchedBoth)
	}
	if out := Render("switch to a plastic frame", both); !strings.Contains(out, "matched lexically and semantically") {
		t.Errorf("the method is not in the rendered result:\n%s", out)
	}
}
