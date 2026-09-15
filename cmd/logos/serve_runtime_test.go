package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
)

// recordingRuntime is an OpenAI-compatible runtime that lists models and
// answers embeddings, recording the model each embeddings request named.
type recordingRuntime struct {
	*httptest.Server
	mu     sync.Mutex
	embeds []string
}

func newRecordingRuntime(t *testing.T, models ...string) *recordingRuntime {
	t.Helper()
	rr := &recordingRuntime{}
	rr.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			data := make([]map[string]string, len(models))
			for i, m := range models {
				data[i] = map[string]string{"id": m}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		rr.mu.Lock()
		rr.embeds = append(rr.embeds, req.Model)
		rr.mu.Unlock()
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"embedding": []float32{1, 0, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(rr.Close)
	return rr
}

func (rr *recordingRuntime) embedded() []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]string(nil), rr.embeds...)
}

// serveOnce runs the MCP server the way `logos mcp serve` builds it, over one
// remember call, and returns what it wrote.
func serveOnce(t *testing.T, vault string) string {
	t.Helper()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	srv, err := newMCPServer(ix.DB, vault)
	if err != nil {
		t.Fatal(err)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember","arguments":{"text":"the staging database lives on the second rack"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := srv.Serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// `logos index` honoured LOGOS_RUNTIME and LOGOS_EMBED; `mcp serve` ignored both
// and went to whatever answered on localhost with the router's model. A runtime
// on another host left the server with no embeddings, and a different embed
// model left stored and query vectors of different lengths, so every cosine was
// 0 and vector retrieval was dead without a word. LOGOS_EMBED=off in a host's
// config did not turn embedding off.
func TestTheMCPServerUsesTheRuntimeAndEmbedModelTheIndexUses(t *testing.T) {
	// The configured runtime also has the router's default embed model, so
	// LOGOS_EMBED=off below is what stops embedding, not a model that is missing.
	configured := newRecordingRuntime(t, "custom-embed", "nomic-embed-text")
	local := newRecordingRuntime(t, "nomic-embed-text")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Fake", URL: local.URL}}
	t.Cleanup(func() { provider.LocalEndpoints = old })

	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_RUNTIME", configured.URL)
	t.Setenv("LOGOS_EMBED", "custom-embed")
	t.Setenv("LOGOS_VAULT", t.TempDir())

	out := serveOnce(t, vaultPath())
	if got := configured.embedded(); len(got) == 0 || got[0] != "custom-embed" {
		t.Errorf("the configured runtime got embeddings for %v, want custom-embed\n%s", got, out)
	}
	if got := local.embedded(); len(got) != 0 {
		t.Errorf("the server embedded on the discovered localhost runtime (%v) despite LOGOS_RUNTIME", got)
	}

	t.Setenv("LOGOS_EMBED", "off")
	before := len(configured.embedded())
	t.Setenv("LOGOS_VAULT", t.TempDir())
	out = serveOnce(t, vaultPath())
	if n := len(configured.embedded()) - before; n != 0 {
		t.Errorf("LOGOS_EMBED=off still sent %d embeddings requests", n)
	}
	if !strings.Contains(out, `"id":2`) {
		t.Errorf("remember did not answer with embeddings off:\n%s", out)
	}
}

// Ollama with only chat models pulled is a common setup. The server dropped the
// error from resolving the embedding model and embedded with it anyway, so
// every tool call sent a request that could only fail, recall fell back to
// every memory unranked, and stderr still said nothing about it. With no
// embedding model the server serves lexical, and a model LOGOS_EMBED names
// still turns embedding back on.
func TestTheMCPServerWithNoEmbeddingModelPulledServesLexicalWithoutAsking(t *testing.T) {
	chatOnly := newRecordingRuntime(t, "llama3.2", "custom-embed")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Fake", URL: chatOnly.URL}}
	t.Cleanup(func() { provider.LocalEndpoints = old })
	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_RUNTIME", "")
	t.Setenv("LOGOS_EMBED", "")
	os.Unsetenv("LOGOS_EMBED")

	t.Setenv("LOGOS_VAULT", t.TempDir())
	out := serveOnce(t, vaultPath())
	if got := chatOnly.embedded(); len(got) != 0 {
		t.Errorf("with no embedding model pulled the server still sent embeddings for %v", got)
	}
	if !strings.Contains(out, `"id":2`) {
		t.Errorf("remember did not answer:\n%s", out)
	}

	t.Setenv("LOGOS_EMBED", "custom-embed")
	t.Setenv("LOGOS_VAULT", t.TempDir())
	serveOnce(t, vaultPath())
	if got := chatOnly.embedded(); len(got) == 0 || got[0] != "custom-embed" {
		t.Errorf("LOGOS_EMBED=custom-embed embedded with %v", got)
	}
}
