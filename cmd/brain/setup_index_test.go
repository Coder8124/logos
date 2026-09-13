package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/provider"
)

// fakeRuntime stands in for Ollama on the endpoint list setup probes, so
// nothing here reaches a model runtime on the developer's machine.
func fakeRuntime(t *testing.T, embeddings http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"nomic-embed-text"}]}`)
			return
		}
		embeddings(w, r)
	}))
	t.Cleanup(srv.Close)
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Fake", URL: srv.URL}}
	t.Cleanup(func() { provider.LocalEndpoints = old })
}

func vaultWithNotes(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := range n {
		body := fmt.Sprintf("# Note %d\n\nsomething worth finding\n", i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("note-%d.md", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Embedding a 2000-note vault took a minute, and setup printed nothing between
// the model line and the index line — a person cannot tell that from a hang,
// and the hosts prompt they came for was waiting behind it.
func TestSetupSaysItIsEmbeddingBeforeItStarts(t *testing.T) {
	fakeRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"embedding": []float32{1, 0, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	vault := vaultWithNotes(t, 3)

	out := captureStdout(t, func() { indexVault(vault) })

	if !strings.Contains(out, "embedding 3 notes") {
		t.Errorf("setup embedded without saying so first:\n%s", out)
	}
}

// Embedding errors were discarded: setup went on to print the note count as
// though the vault were fully searchable.
func TestSetupReportsAnEmbeddingFailure(t *testing.T) {
	fakeRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	})
	vault := vaultWithNotes(t, 2)

	out := captureStdout(t, func() { indexVault(vault) })

	if !strings.Contains(out, "embedding failed") {
		t.Errorf("an embedding failure was swallowed:\n%s", out)
	}
}
