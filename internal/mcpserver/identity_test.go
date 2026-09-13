package mcpserver

import (
	"testing"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
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

	// Read the review queue, not active memory: an MCP client's remember is
	// quarantined by default (quarantineMCP in server.go), and attribution has
	// to survive the wait — knowing which agent proposed a fact is part of
	// deciding whether to accept it.
	mems, err := memory.Pending(db)
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

	mems, err := memory.Pending(db)
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

// A checkpoint is the handoff: "who stopped here" is the first thing read on
// the other side, so it takes the same handshake name remember does instead of
// depending on the model remembering to pass an agent argument.
func TestCheckpointAndNotesWithoutAnAgentArgumentAreCreditedToTheHost(t *testing.T) {
	c, db, vaultDir := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "1.2.3"},
	})
	c.notify("notifications/initialized", nil)

	if out, isErr := c.callText(t, "note_progress", map[string]any{
		"project": "my-app", "text": "the flaky test is a timezone bug",
	}); isErr {
		t.Fatalf("note_progress reported error: %s", out)
	}
	notes, err := session.Uncommitted(db, "my-app")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Agent != "cursor" {
		t.Errorf("notes = %+v, want one note by %q", notes, "cursor")
	}

	if out, isErr := c.callText(t, "checkpoint", map[string]any{
		"project": "my-app", "task": "fix the flaky test", "next": "pin TZ in CI",
	}); isErr {
		t.Fatalf("checkpoint reported error: %s", out)
	}
	cp, err := session.Latest(vaultDir, "my-app")
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil || cp.Agent != "cursor" {
		t.Errorf("checkpoint = %+v, want Agent %q from clientInfo.name", cp, "cursor")
	}
}

// A name the model does pass still wins: it is how a host that sends no
// clientInfo, or a subagent inside one, says who it is.
func TestAnExplicitAgentArgumentStillNamesTheCheckpoint(t *testing.T) {
	c, _, vaultDir := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "1.2.3"},
	})
	c.notify("notifications/initialized", nil)

	if out, isErr := c.callText(t, "checkpoint", map[string]any{
		"project": "my-app", "agent": "reviewer", "task": "review", "next": "merge",
	}); isErr {
		t.Fatalf("checkpoint reported error: %s", out)
	}
	cp, err := session.Latest(vaultDir, "my-app")
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil || cp.Agent != "reviewer" {
		t.Errorf("checkpoint = %+v, want Agent %q", cp, "reviewer")
	}
}

// Hosts send whatever their SDK calls itself, so one tool used to appear as
// "cursor-vscode" on a memory and "cursor" in the docs, and a cross-agent trail
// read as more agents than there were.
func TestEachHostIsRecordedUnderOneShortName(t *testing.T) {
	for raw, want := range map[string]string{
		"claude-code":        "claude-code",
		"cursor-vscode":      "cursor",
		"codex-mcp-client":   "codex",
		"Visual Studio Code": "copilot",
		"Cline":              "cline",
		"claude-ai":          "desktop",
		"some-new-host":      "some-new-host",
	} {
		params := []byte(`{"clientInfo":{"name":"` + raw + `"}}`)
		if got := clientInfoFromInitialize(params); got != want {
			t.Errorf("clientInfo %q recorded as %q, want %q", raw, got, want)
		}
	}
}

// A model under Claude Code types "claude" as its agent; that is the host's
// own name cut short, not a different agent, and must not split the trail.
func TestAnAgentArgumentThatAbbreviatesTheHostIsRecordedAsTheHost(t *testing.T) {
	c, _, vaultDir := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "claude-code", "version": "2.0.0"},
	})
	c.notify("notifications/initialized", nil)

	if out, isErr := c.callText(t, "checkpoint", map[string]any{
		"project": "my-app", "agent": "claude", "task": "review", "next": "merge",
	}); isErr {
		t.Fatalf("checkpoint reported error: %s", out)
	}
	cp, err := session.Latest(vaultDir, "my-app")
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil || cp.Agent != "claude-code" {
		t.Errorf("checkpoint = %+v, want Agent %q", cp, "claude-code")
	}
}
