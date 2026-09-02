package mcpserver

import (
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

// The premise under test: remember records who is calling by reading the MCP
// handshake, not by trusting a tool argument the model might get wrong or skip
// entirely. No test here ever passes an "agent" argument to remember — there
// is no such argument on that tool — so the only way Agent ends up populated
// is clientInfoFromInitialize doing its job.

func TestRememberRecordsClientAgentFromHandshake(t *testing.T) {
	c, db, _ := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "1.2.3"},
	})
	c.notify("notifications/initialized", nil)

	if out, isErr := c.callText(t, "remember", map[string]any{
		"text": "the release branch is cut on Fridays", "kind": "fact",
	}); isErr {
		t.Fatalf("remember reported error: %s", out)
	}

	mems, err := memory.All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 {
		t.Fatalf("want 1 memory, got %d", len(mems))
	}
	if mems[0].Agent != "cursor" {
		t.Errorf("agent = %q, want %q (from clientInfo.name, not asked of the model)", mems[0].Agent, "cursor")
	}
}

// A host that omits clientInfo entirely — or sends no name — must not crash
// the handshake, and the resulting memory just carries no agent. Silence is a
// legitimate answer; a guessed name would not be.
func TestRememberToleratesMissingClientInfo(t *testing.T) {
	c, db, _ := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
	})
	c.notify("notifications/initialized", nil)

	if out, isErr := c.callText(t, "remember", map[string]any{
		"text": "no host identity was ever given", "kind": "fact",
	}); isErr {
		t.Fatalf("remember reported error: %s", out)
	}

	mems, err := memory.All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 1 {
		t.Fatalf("want 1 memory, got %d", len(mems))
	}
	if mems[0].Agent != "" {
		t.Errorf("agent = %q, want empty when the host sent no clientInfo", mems[0].Agent)
	}
}

// Two different hosts sharing one vault must not be able to overwrite each
// other's attribution: memory stored under one handshake keeps that agent
// forever, even after another connects.
func TestClientInfoParsingIsIsolatedPerSession(t *testing.T) {
	if got := clientInfoFromInitialize(nil); got != "" {
		t.Errorf("nil params should yield empty agent, got %q", got)
	}
	if got := clientInfoFromInitialize([]byte(`not json`)); got != "" {
		t.Errorf("unparsable params should yield empty agent, got %q", got)
	}
	if got := clientInfoFromInitialize([]byte(`{"clientInfo":{"name":"  claude-code  "}}`)); got != "claude-code" {
		t.Errorf("clientInfo.name should be trimmed, got %q", got)
	}
}
