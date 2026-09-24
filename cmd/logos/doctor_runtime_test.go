package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/health"
)

// The server resolves its runtime through LOGOS_RUNTIME, and a configured
// runtime that does not answer means no runtime — it never falls back to
// whatever is on localhost. Doctor probed localhost regardless, so with
// LOGOS_RUNTIME pointing at a stopped remote runtime it reported the local
// Ollama as healthy while every context call ran without embeddings: the one
// command meant to explain the degraded search said nothing was wrong.
func TestDoctorReportsTheRuntimeTheServerWouldUse(t *testing.T) {
	fakeRuntime(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", "http://127.0.0.1:1")

	rep := gatherHealth()

	c, ok := checkNamed(rep, "model runtime")
	if !ok {
		t.Fatal("no model runtime check in the report")
	}
	if !strings.HasPrefix(c.Detail, "none") {
		t.Errorf("model runtime is %q; the server would use none, because LOGOS_RUNTIME names a runtime that does not answer", c.Detail)
	}
	if c.State != health.Failed || !strings.Contains(c.Fix, "LOGOS_RUNTIME") {
		t.Errorf("model runtime is %v with fix %q; a runtime the user named and cannot reach is actionable, and the fix is about it, not about installing Ollama", c.State, c.Fix)
	}
}
