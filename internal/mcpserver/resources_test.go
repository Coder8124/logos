package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// resourceContent reads a resources/read result's first text content block.
func resourceContent(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var res struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("bad resources/read result: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("resources/read returned %d content blocks, want 1", len(res.Contents))
	}
	return res.Contents[0].Text
}

// The server must advertise the resources capability at handshake, or a host
// has no reason to ever call resources/list.
func TestHandshakeAdvertisesResourcesCapability(t *testing.T) {
	c, _, _ := startServer(t)
	raw := c.req("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	var init struct {
		Capabilities struct {
			Resources map[string]any `json:"resources"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatal(err)
	}
	if init.Capabilities.Resources == nil {
		t.Error("server must advertise a resources capability")
	}
	c.notify("notifications/initialized", nil)
}

func TestResourcesListNamesMemoriesAndProjects(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	raw := c.req("resources/list", nil)
	var res struct {
		Resources []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"logos://memories": true, "logos://projects": true}
	if len(res.Resources) != len(want) {
		t.Fatalf("got %d resources, want %d", len(res.Resources), len(want))
	}
	for _, r := range res.Resources {
		if !want[r.URI] {
			t.Errorf("unexpected resource %q", r.URI)
		}
	}
}

func TestResourceTemplatesListNamesMemoryDiff(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	raw := c.req("resources/templates/list", nil)
	var res struct {
		ResourceTemplates []struct {
			URITemplate string `json:"uriTemplate"`
		} `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ResourceTemplates) != 1 || res.ResourceTemplates[0].URITemplate != "logos://memory-diff{?subject,days}" {
		t.Errorf("resourceTemplates = %+v, want one memory-diff template", res.ResourceTemplates)
	}
}

// Reading the memories resource must return the same content the equivalent
// tool call returns — a host choosing the cheaper resource path must not see
// a different answer than one that kept calling the tool.
func TestReadResourceMemoriesMatchesListMemoriesTool(t *testing.T) {
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
	handshake(t, c)

	if _, isErr := c.callText(t, "remember", map[string]any{
		"text": "The user prefers dark mode.",
		"kind": "preference",
	}); isErr {
		t.Fatal("remember failed")
	}

	toolOut, isErr := c.callText(t, "list_memories", map[string]any{})
	if isErr {
		t.Fatalf("list_memories errored: %s", toolOut)
	}

	raw := c.req("resources/read", map[string]any{"uri": "logos://memories"})
	resourceOut := resourceContent(t, raw)

	if resourceOut != toolOut {
		t.Errorf("resources/read logos://memories = %q, want it to match list_memories tool output %q", resourceOut, toolOut)
	}
}

func TestReadResourceProjectsMatchesListProjectsTool(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	toolOut, isErr := c.callText(t, "list_projects", map[string]any{})
	if isErr {
		t.Fatalf("list_projects errored: %s", toolOut)
	}

	raw := c.req("resources/read", map[string]any{"uri": "logos://projects"})
	resourceOut := resourceContent(t, raw)

	if resourceOut != toolOut {
		t.Errorf("resources/read logos://projects = %q, want it to match list_projects tool output %q", resourceOut, toolOut)
	}
}

// The memory-diff resource has to parse the query string a client expands
// from {?subject,days} itself — there is no separate params object the way a
// tool call gets one.
func TestReadResourceMemoryDiffParsesSubjectAndDays(t *testing.T) {
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
	handshake(t, c)

	if _, isErr := c.callText(t, "remember", map[string]any{
		"text": "Sarah is the CFO.",
		"kind": "person",
	}); isErr {
		t.Fatal("remember failed")
	}

	raw := c.req("resources/read", map[string]any{"uri": "logos://memory-diff?subject=Sarah&days=1"})
	out := resourceContent(t, raw)
	if !strings.Contains(out, "Sarah") {
		t.Errorf("memory-diff resource should surface the Sarah fact, got: %q", out)
	}

	// The bare form, with no query tail at all, must default exactly the way
	// the tool call's own defaults do (every subject, 7 days) rather than
	// erroring on the missing "?".
	raw = c.req("resources/read", map[string]any{"uri": "logos://memory-diff"})
	out = resourceContent(t, raw)
	if !strings.Contains(out, "Sarah") {
		t.Errorf("bare memory-diff resource should still surface recent changes, got: %q", out)
	}
}

// An unknown resource URI is a protocol error, not a tool-shaped isError
// result — resources/read has no isError convention, and a client that never
// checks for a JSON-RPC error field deserves a response it cannot mistake for
// content. c.req would fail the test on any error response, so this calls the
// wire directly, the same way TestUnknownMethodReturnsProtocolError does.
func TestReadResourceUnknownURIIsAProtocolError(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	c.id++
	send(t, c.w, map[string]any{
		"jsonrpc": "2.0", "id": c.id, "method": "resources/read",
		"params": map[string]any{"uri": "logos://nonsense"},
	})
	for c.sc.Scan() {
		line := strings.TrimSpace(c.sc.Text())
		if line == "" {
			continue
		}
		var resp struct {
			ID    int `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &resp) != nil || resp.ID != c.id {
			continue
		}
		if resp.Error == nil {
			t.Error("unknown resource uri should return a protocol error, got a result")
		}
		return
	}
	t.Fatal("no response to resources/read with an unknown uri")
}
