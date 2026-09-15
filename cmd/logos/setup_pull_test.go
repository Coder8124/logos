package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/provider"
)

// emptyRuntime answers the model listing with nothing pulled, and counts any
// request to Ollama's pull endpoint.
func emptyRuntime(t *testing.T, name string) *atomic.Int32 {
	t.Helper()
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		if r.URL.Path == "/api/pull" {
			pulls.Add(1)
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: name, URL: srv.URL + "/v1"}}
	t.Cleanup(func() { provider.LocalEndpoints = old })
	return &pulls
}

// Only Ollama has /api/pull. LM Studio, Jan and Msty answered the offered pull
// with "failed: 404 Not Found", after the user had said yes to it.
func TestSetupDoesNotOfferAnOllamaPullToAnotherRuntime(t *testing.T) {
	t.Setenv("LOGOS_EMBED", "")
	t.Setenv("LOGOS_VAULT", t.TempDir())
	pulls := emptyRuntime(t, "LM Studio")

	out := captureStdout(t, func() { checkRuntime(true, false) })

	if pulls.Load() != 0 {
		t.Errorf("setup asked LM Studio for an Ollama pull:\n%s", out)
	}
	if !strings.Contains(out, "load "+defaultEmbedModel+" in LM Studio") {
		t.Errorf("setup did not say which model to load in LM Studio:\n%s", out)
	}
}

// The pull used the default client, which has no timeout: an Ollama that
// accepted the connection and never answered left setup waiting forever.
func TestAPullNobodyAnswersDoesNotHangSetup(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	old := pullTimeout
	pullTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pullTimeout = old })

	done := make(chan error, 1)
	go func() { done <- pullModel(srv.URL+"/v1", "nomic-embed-text", func(int) {}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a pull that never answered reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pull was still waiting 5s after a 100ms timeout")
	}
}

// A 270 MB download printed "pulling … " and nothing else until it finished,
// which is indistinguishable from a hang.
func TestAPullShowsHowFarAlongItIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"status":"pulling abc","total":200,"completed":100}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))
	t.Cleanup(srv.Close)

	out := captureStdout(t, func() { pull(srv.URL+"/v1", "nomic-embed-text") })

	if !strings.Contains(out, "50%") || !strings.HasSuffix(strings.TrimSpace(out), "done") {
		t.Errorf("the pull did not show its progress and then finish:\n%q", out)
	}
}
