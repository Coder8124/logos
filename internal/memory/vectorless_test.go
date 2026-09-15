package memory

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// embedRuntime answers every embeddings request with the same vector, or with
// 404 when missing is true — Ollama with no embedding model pulled.
func embedRuntime(t *testing.T, missing bool) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if missing {
			http.Error(w, `{"error":"model \"nomic-embed-text\" not found"}`, http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":[{"embedding":[1,0,0]}]}`))
	}))
	t.Cleanup(srv.Close)
	return provider.New("Fake", srv.URL, "")
}

// A memory stored while no runtime answered has no vector, and recall skipped
// every such row once a runtime was up — the lexical arm never saw it either.
// A user who started without Ollama and installed it a week later lost that
// week from recall and resume, until a `logos index` nothing told them to run.
func TestAMemoryStoredWithoutAVectorIsRecalledOnceARuntimeIsUp(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "The auth service rotates its signing keys weekly", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "Deploys go through the staging cluster first because prod has no rollback", Fact, 0.5, nil)

	for name, recall := range map[string]func() ([]Memory, error){
		"Recall": func() ([]Memory, error) {
			return Recall(db, embedRuntime(t, false), "nomic-embed-text", "deploy rollback staging", 5)
		},
		"RecallInProject": func() ([]Memory, error) {
			return RecallInProject(db, embedRuntime(t, false), "nomic-embed-text", "deploy rollback staging", "kestrel", 5)
		},
	} {
		got, err := recall()
		if err != nil {
			t.Fatal(err)
		}
		if !containsText(got, "staging cluster") {
			t.Errorf("%s left out the memory stored without a vector: %v", name, texts(got))
		}
	}
}

// With no embedding model, recall fell back to every memory in the project,
// salience-first, ignoring limit: `recall "who owns billing" limit 3` on forty
// memories returned forty lines, billing first only because it was stored first.
func TestRecallWithoutEmbeddingsRanksByKeywordAndHonoursTheLimit(t *testing.T) {
	db := testDB(t)
	for _, module := range []string{"search", "auth", "payments", "reports", "mail", "billing", "export", "import", "admin", "audit"} {
		for _, fact := range []string{"%s is owned by the platform team", "%s deploys on Tuesdays", "%s logs to the shared bucket", "%s has a runbook in the wiki"} {
			storeVec(t, db, fmt.Sprintf(fact, module), Fact, 0.5, nil)
		}
	}

	for name, recall := range map[string]func() ([]Memory, error){
		"no runtime": func() ([]Memory, error) { return RecallInProject(db, nil, "", "who owns billing", "", 3) },
		"no embedding model": func() ([]Memory, error) {
			return Recall(db, embedRuntime(t, true), "nomic-embed-text", "who owns billing", 3)
		},
	} {
		got, err := recall()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Errorf("%s: recall with limit 3 returned %d memories", name, len(got))
		}
		if len(got) == 0 || !strings.Contains(got[0].Text, "billing") {
			t.Errorf("%s: first result is not about billing: %v", name, texts(got))
		}
	}
}

func containsText(mems []Memory, s string) bool {
	for _, m := range mems {
		if strings.Contains(m.Text, s) {
			return true
		}
	}
	return false
}

func texts(mems []Memory) []string {
	out := make([]string, len(mems))
	for i, m := range mems {
		out[i] = m.Text
	}
	return out
}
