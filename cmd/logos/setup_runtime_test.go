package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// configuredRuntime is a runtime reachable only through LOGOS_RUNTIME, with
// nothing on the localhost ports discovery probes — a runtime on another host.
func configuredRuntime(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"nomic-embed-text"}]}`)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range data {
			data[i] = map[string]any{"embedding": []float32{1, 0, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })
	t.Setenv("LOGOS_RUNTIME", srv.URL)
	t.Setenv("LOGOS_EMBED", "")
	return srv.URL
}

// Setup probed only localhost: a user whose runtime is on another host, named
// by LOGOS_RUNTIME as doctor and index honour, was told nothing about it.
func TestSetupReportsTheRuntimeLogosRuntimeNames(t *testing.T) {
	url := configuredRuntime(t)
	t.Setenv("LOGOS_VAULT", t.TempDir())

	out := captureStdout(t, func() { checkRuntime(true, true) })

	if !strings.Contains(out, url) {
		t.Errorf("setup did not report the runtime LOGOS_RUNTIME names (%s):\n%s", url, out)
	}
}

// And indexed without embeddings, so search was lexical until a later
// `logos index` that did honour the setting.
func TestSetupEmbedsWithTheRuntimeLogosRuntimeNames(t *testing.T) {
	configuredRuntime(t)
	vault := vaultWithNotes(t, 2)

	out := captureStdout(t, func() { indexVault(vault) })

	if !strings.Contains(out, "embedding 2 notes") {
		t.Errorf("setup did not embed with the runtime LOGOS_RUNTIME names:\n%s", out)
	}
}
