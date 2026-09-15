package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
)

// stallingRuntime lists an embedding model at once and holds every embeddings
// request until the test ends — Ollama loading the model behind a large one, or
// wedged after sleep.
// Release it before the client closes, or shutdown waits on the held request.
func stallingRuntime(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	release := make(chan struct{})
	rt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"nomic-embed-text"}]}`))
			return
		}
		<-release
		http.Error(w, "released", http.StatusServiceUnavailable)
	}))
	t.Cleanup(rt.Close)
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	return rt, free
}

// withRuntime builds the server the way `logos mcp serve` does, against rt.
func withRuntime(t *testing.T, rt *httptest.Server) func(*Server) {
	t.Helper()
	old := provider.LocalEndpoints
	provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Stall", URL: rt.URL}}
	t.Cleanup(func() { provider.LocalEndpoints = old })
	t.Setenv("LOGOS_RUNTIME", "")
	r, err := router.New(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return func(srv *Server) {
		built := New(srv.DB, r, srv.vault)
		srv.embed, srv.embedModel = built.embed, built.embedModel
	}
}

func setEmbedTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := embedTimeout
	embedTimeout = d
	t.Cleanup(func() { embedTimeout = old })
}

// Embeddings shared the 300 s client sized for generation, and resume embeds
// three times, so a runtime that listed its models and then never answered held
// resume for up to fifteen minutes. The host gave up long before and showed a
// timeout, with nothing saying the model was the cause; quitting Ollama would
// have fixed it at once.
func TestAStalledModelRuntimeCostsResumeTheShortTimeoutAndSaysSo(t *testing.T) {
	setEmbedTimeout(t, 300*time.Millisecond)
	rt, release := stallingRuntime(t)
	c, _ := startAsync(t, withRuntime(t, rt))
	defer release()

	start := time.Now()
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resume","arguments":{"project":"kestrel-one"}}}`)
	line, ok := c.await(t, `"id":2`, 5*time.Second)
	if !ok {
		t.Fatal("resume did not answer within 5s against a stalled runtime")
	}
	// One timeout, not three: after the first, the runtime is treated as down.
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("resume took %v, want about one 300ms timeout", took)
	}
	if !strings.Contains(line, "didn't answer") {
		t.Errorf("resume fell back to lexical without saying why:\n%s", line)
	}
}

// One loop handled every request inline, so a tool waiting on the runtime held
// ping behind it too, and a host that pings to check liveness restarted a
// server that was only waiting.
func TestPingIsAnsweredWhileAToolWaitsOnTheRuntime(t *testing.T) {
	setEmbedTimeout(t, time.Minute)
	rt, release := stallingRuntime(t)
	c, _ := startAsync(t, withRuntime(t, rt))
	defer release()

	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resume","arguments":{"project":"kestrel-one"}}}`)
	time.Sleep(100 * time.Millisecond)
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if _, ok := c.await(t, `"id":3`, time.Second); !ok {
		t.Error("ping waited behind a tool call")
	}
}
