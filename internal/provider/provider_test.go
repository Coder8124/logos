package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every test here talks to an httptest server on loopback: nothing leaves the
// machine, and no test depends on a model runtime being installed.

// A runtime that listed its models and then never answered embeddings held an
// MCP tool call for minutes, three times over in resume. An Interactive
// provider fails after its timeout and then skips the runtime for the
// cooloff, so the calls after it fail at once instead of each paying again.
func TestAnInteractiveProviderThatTimesOutSkipsEmbeddingsUntilItCoolsOff(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	p := New("Ollama", srv.URL+"/v1", "").Interactive(50 * time.Millisecond)
	if _, err := p.Embed("nomic-embed-text", []string{"waveguide"}); err == nil {
		t.Fatal("a runtime that never answered returned embeddings")
	}
	start := time.Now()
	_, err := p.Embed("nomic-embed-text", []string{"waveguide"})
	if err == nil || !strings.Contains(err.Error(), "cooled off") {
		t.Fatalf("second call during the cooloff: err = %v, want one that says it was skipped", err)
	}
	if waited := time.Since(start); waited > 40*time.Millisecond {
		t.Errorf("the skipped call waited %s; it should not reach the runtime at all", waited)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the runtime got %d requests, want 1 — the cooloff did not hold", n)
	}
	if n := p.Stalls(); n != 2 {
		t.Errorf("Stalls = %d, want 2 (the timeout and the skip), so the caller can say the result was built without them", n)
	}
}

func TestAProviderThatIsNotInteractiveReportsNoStalls(t *testing.T) {
	if n := New("Ollama", "http://127.0.0.1:1/v1", "").Stalls(); n != 0 {
		t.Errorf("Stalls = %d, want 0", n)
	}
	var none *Provider
	if n := none.Stalls(); n != 0 {
		t.Errorf("Stalls on a nil provider = %d, want 0", n)
	}
}

// The user named where the model runs; quietly using whatever answers on
// localhost instead is how vectors end up from two models.
func TestAConfiguredRuntimeThatDoesNotAnswerIsNoneRatherThanADiscoveredOne(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/v1"
	srv.Close()
	t.Setenv("LOGOS_RUNTIME", url)
	if got := Resolve(); got != nil {
		t.Errorf("Resolve with a dead LOGOS_RUNTIME = %+v, want nothing", got)
	}
}

// A machine with Ollama up had no way to show what logos does without one: the
// ports are fixed, and a dead LOGOS_RUNTIME only reached the callers that ask
// Resolve, not setup's own Discover. "off" has to reach both.
func TestRuntimeOffFindsNothingEvenWithARuntimeAnswering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"nomic-embed-text"}]}`)
	}))
	defer srv.Close()
	old := LocalEndpoints
	LocalEndpoints = []LocalEndpoint{{"Ollama", srv.URL + "/v1"}}
	t.Cleanup(func() { LocalEndpoints = old })
	if len(Discover()) != 1 {
		t.Fatal("the fake runtime was not discovered, so this test proves nothing")
	}

	t.Setenv("LOGOS_RUNTIME", "off")
	if got := Discover(); got != nil {
		t.Errorf("Discover with LOGOS_RUNTIME=off = %+v, want nothing", got)
	}
	if got := Resolve(); got != nil {
		t.Errorf("Resolve with LOGOS_RUNTIME=off = %+v, want nothing", got)
	}
	if p := Configured(); p != nil {
		t.Errorf("Configured with LOGOS_RUNTIME=off = %s, want none — \"off\" is not a URL", p.BaseURL)
	}
}

func TestAConfiguredRuntimeIsTheOnlyCandidateAndGetsItsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sekrit" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"qwen3.6"},{"id":"nomic-embed-text"}]}`)
	}))
	defer srv.Close()
	t.Setenv("LOGOS_RUNTIME", srv.URL+"/v1/")
	t.Setenv("LOGOS_RUNTIME_KEY", "sekrit")

	got := Resolve()
	if len(got) != 1 {
		t.Fatalf("Resolve = %d runtimes, want only the configured one", len(got))
	}
	if got[0].Provider.Name != "configured" || strings.Join(got[0].Models, ",") != "qwen3.6,nomic-embed-text" {
		t.Errorf("Resolve = %s with %v", got[0].Provider.Name, got[0].Models)
	}
}

func TestAnErrorStatusNamesTheRuntimeAndWhatItSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `model "nomic-embed-text" not found, try pulling it first`, http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New("LM Studio", srv.URL+"/v1", "").Embed("nomic-embed-text", []string{"x"})
	if err == nil {
		t.Fatal("a 404 returned embeddings")
	}
	for _, want := range []string{"LM Studio", "404", "try pulling it first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// A runtime that drops an input would shift every vector after it onto the
// wrong note.
func TestEmbedRefusesAnAnswerWithTheWrongNumberOfVectors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2]}]}`)
	}))
	defer srv.Close()
	if _, err := New("Ollama", srv.URL+"/v1", "").Embed("m", []string{"a", "b"}); err == nil {
		t.Error("two inputs and one vector came back as success")
	}
}

func TestEmbeddingNothingAsksTheRuntimeNothing(t *testing.T) {
	got, err := New("Ollama", "http://127.0.0.1:1/v1", "").Embed("m", nil)
	if err != nil || got != nil {
		t.Errorf("Embed(nil) = %v, %v; want nothing and no request", got, err)
	}
}

// Small local models are unreliable at tool calls and near-perfect when the
// sampler enforces JSON, so every extraction relies on the schema arriving.
func TestChatSendsTheSchemaAsConstrainedDecoding(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body: %v", err)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`)
	}))
	defer srv.Close()

	schema := map[string]any{"type": "object"}
	got, err := New("Ollama", srv.URL+"/v1", "").Chat("qwen3.6", "sys", "user", schema)
	if err != nil || got != `{"ok":true}` {
		t.Fatalf("Chat = %q, %v", got, err)
	}
	rf, _ := body["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || js["schema"] == nil {
		t.Errorf("response_format = %v, want a strict json_schema", body["response_format"])
	}
}

func TestChatWithNoChoicesIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()
	if _, err := New("Ollama", srv.URL+"/v1", "").Chat("m", "s", "u", nil); err == nil {
		t.Error("an answer with no choices came back as an empty success")
	}
}

func TestChatStreamAssemblesServerSentEventsAndStopsAtDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("a runtime that is not Ollama was asked at %s", r.URL.Path)
		}
		for _, line := range []string{
			`data: {"choices":[{"delta":{"content":"Extruded"}}]}`,
			`: keep-alive`,
			`data: {"choices":[{"delta":{"content":" frames."}}]}`,
			`data: [DONE]`,
			`data: {"choices":[{"delta":{"content":" after done"}}]}`,
		} {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	var tokens []string
	got, err := New("LM Studio", srv.URL+"/v1", "").ChatStream("m", []Msg{{Role: "user", Content: "q"}},
		func(tok string) { tokens = append(tokens, tok) })
	if err != nil || got != "Extruded frames." {
		t.Fatalf("ChatStream = %q, %v", got, err)
	}
	if len(tokens) != 2 {
		t.Errorf("onToken got %q, want each token as it arrived", tokens)
	}
}

// A reasoning model on /v1 spends its whole budget thinking and answers
// nothing, so Ollama goes through /api/chat with think bounded — and falls
// back to /v1 when the native endpoint refuses, for a model that rejects
// `think`.
func TestOllamaStreamsThroughTheNativeEndpointWithThinkingBounded(t *testing.T) {
	var think any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("asked at %s, want /api/chat", r.URL.Path)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		think = body["think"]
		fmt.Fprintln(w, `{"message":{"content":"Extruded"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"content":" frames."},"done":true}`)
	}))
	defer srv.Close()

	got, err := New("Ollama", srv.URL+"/v1", "").ChatStream("qwen3.6", []Msg{{Role: "user", Content: "q"}}, func(string) {})
	if err != nil || got != "Extruded frames." {
		t.Fatalf("ChatStream = %q, %v", got, err)
	}
	if think != "low" {
		t.Errorf("think = %v, want the default low", think)
	}
}

func TestOllamaFallsBackToV1WhenTheNativeEndpointRefuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			http.Error(w, `"qwen2" does not support thinking`, http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"answer"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()

	got, err := New("Ollama", srv.URL+"/v1", "").ChatStream("qwen2", []Msg{{Role: "user", Content: "q"}}, func(string) {})
	if err != nil || got != "answer" {
		t.Errorf("ChatStream = %q, %v; want the /v1 answer", got, err)
	}
}

func TestThinkDefaultOptsOllamaBackOutOfTheNativeEndpoint(t *testing.T) {
	for think, native := range map[string]bool{"": true, "off": true, "high": true, "default": false, "bogus": false} {
		p := New("Ollama", "http://127.0.0.1:1/v1", "")
		p.Think = think
		if _, ok := p.thinkValue(); ok != native {
			t.Errorf("Think %q: native = %v, want %v", think, ok, native)
		}
	}
	if _, ok := New("LM Studio", "http://127.0.0.1:1/v1", "").thinkValue(); ok {
		t.Error("a runtime that is not Ollama was sent to /api/chat")
	}
}
