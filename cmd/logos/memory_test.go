package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/provider"
)

// Most memories in an existing vault predate the Agent field, and every
// memory the CLI itself writes has none at all — logos memory add is not any
// particular coding agent. The listing has to say so plainly rather than
// leaving a trailing "· " that reads as a rendering bug.
func TestAgentLabelRendersEmptyCleanly(t *testing.T) {
	if got := agentLabel(""); got != "-" {
		t.Errorf("agentLabel(\"\") = %q, want a placeholder, not a blank string", got)
	}
	if got := agentLabel("   "); got != "-" {
		t.Errorf("agentLabel of whitespace-only = %q, want a placeholder", got)
	}
	if got := agentLabel("claude-code"); got != "claude-code" {
		t.Errorf("agentLabel(%q) = %q, want the name unchanged", "claude-code", got)
	}
}

// Storing a fact needs no model: without a runtime it dedups by exact text
// instead of by meaning, which is what the MCP remember tool already does. The
// CLI refused outright with "no local model runtime found", so on any machine
// without Ollama or LM Studio running — a CI runner, a fresh laptop — `logos
// memory add` could not write anything at all.
func TestMemoryAddStoresAFactWithNoModelRuntimeRunning(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_RUNTIME", "")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })

	const fact = "the release runner has no model runtime"
	if err := memoryCmd([]string{"add", fact}); err != nil {
		t.Fatalf("memory add with no runtime: %v", err)
	}
	out := captureStdout(t, func() {
		if err := memoryCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, fact) {
		t.Errorf("the fact was not stored:\n%s", out)
	}
}
