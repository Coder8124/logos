package mcpserver

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// An end-to-end test of the MCP memory server: a real client speaks the wire
// protocol to a real Server over pipes — the same newline-delimited JSON-RPC an
// external host (Claude Desktop, Cursor) would use — and drives the full
// lifecycle: handshake, tool discovery, then a remember→recall→list→forget round
// trip through actual SQLite. No model is required: with a nil embedder the
// store keeps working (recall falls back to salience order), so the transport,
// dispatch, and persistence are all exercised deterministically.

// testClient is a minimal MCP client bound to the server's pipes. It mirrors the
// real client in internal/business, kept local so the test proves the server
// against an independent implementation of the protocol.
type testClient struct {
	t  *testing.T
	w  io.Writer
	sc *bufio.Scanner
	id int
}

func (c *testClient) req(method string, params any) json.RawMessage {
	c.t.Helper()
	c.id++
	send(c.t, c.w, map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	// Read until the response with our id (skipping any notifications).
	for c.sc.Scan() {
		line := strings.TrimSpace(c.sc.Text())
		if line == "" {
			continue
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &resp) != nil || resp.ID != c.id {
			continue
		}
		if resp.Error != nil {
			c.t.Fatalf("%s returned error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result
	}
	c.t.Fatalf("%s: no response (scanner err: %v)", method, c.sc.Err())
	return nil
}

func (c *testClient) notify(method string, params any) {
	send(c.t, c.w, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// callText invokes a tool and returns its flattened text content plus isError.
func (c *testClient) callText(t *testing.T, tool string, args map[string]any) (string, bool) {
	raw := c.req("tools/call", map[string]any{"name": tool, "arguments": args})
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("%s: bad tool result: %v", tool, err)
	}
	var b strings.Builder
	for _, p := range res.Content {
		b.WriteString(p.Text)
	}
	return b.String(), res.IsError
}

func send(t *testing.T, w io.Writer, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// startServer wires a Server (nil embedder, temp SQLite) to a client over two
// pipes and runs Serve in the background. Returns the client and the DB.
func startServer(t *testing.T) (*testClient, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// client → server and server → client.
	toSrv, fromClient := io.Pipe()
	toClient, fromSrv := io.Pipe()
	srv := &Server{DB: db}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(toSrv, fromSrv) }()
	t.Cleanup(func() {
		fromClient.Close() // closes server's stdin → Serve returns
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down after stdin close")
		}
	})

	sc := bufio.NewScanner(toClient)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	return &testClient{t: t, w: fromClient, sc: sc}, db
}

func handshake(t *testing.T, c *testClient) {
	t.Helper()
	raw := c.req("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if init.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", init.ProtocolVersion, protocolVersion)
	}
	if init.ServerInfo.Name != "brain-memory" {
		t.Errorf("serverInfo.name = %q, want brain-memory", init.ServerInfo.Name)
	}
	if init.Capabilities.Tools == nil {
		t.Error("server must advertise a tools capability")
	}
	c.notify("notifications/initialized", nil)
}

func TestHandshakeAndToolDiscovery(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	raw := c.req("tools/list", nil)
	var res struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"remember": true, "recall": true, "list_memories": true, "forget": true,
		"context_pack": true, "memory_diff": true, "list_projects": true,
	}
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(want))
	}
	for _, tool := range res.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		if tool.Description == "" || len(tool.InputSchema) == 0 {
			t.Errorf("tool %q missing description or schema", tool.Name)
		}
	}
}

// The broadened memory-layer surface: what an external app writes via remember
// shows up when it asks memory_diff what changed.
func TestMemoryDiffTool(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	if _, isErr := c.callText(t, "remember", map[string]any{
		"text": "The user switched to Neovim.",
		"kind": "preference",
	}); isErr {
		t.Fatal("remember failed")
	}
	out, isErr := c.callText(t, "memory_diff", map[string]any{"days": 1})
	if isErr {
		t.Fatalf("memory_diff errored: %s", out)
	}
	if !strings.Contains(out, "Neovim") {
		t.Errorf("diff should surface the newly remembered fact, got: %q", out)
	}
}

func TestRememberRecallRoundTrip(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	if out, isErr := c.callText(t, "remember", map[string]any{
		"text": "The user prefers short, direct replies.",
		"kind": "preference",
	}); isErr {
		t.Fatalf("remember reported error: %s", out)
	}

	// A second, unrelated fact so recall/list have something to choose among.
	c.callText(t, "remember", map[string]any{"text": "Sarah Chen is the CFO.", "kind": "person"})

	out, isErr := c.callText(t, "recall", map[string]any{"query": "how should I reply to the user?"})
	if isErr {
		t.Fatalf("recall reported error: %s", out)
	}
	if !strings.Contains(out, "short") {
		t.Errorf("recall did not surface the stored preference; got:\n%s", out)
	}

	list, _ := c.callText(t, "list_memories", nil)
	if !strings.Contains(list, "CFO") || !strings.Contains(list, "short") {
		t.Errorf("list_memories missing stored entries; got:\n%s", list)
	}
}

func TestForgetRemovesMemory(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	c.callText(t, "remember", map[string]any{"text": "Temporary note to be deleted.", "kind": "fact"})
	list, _ := c.callText(t, "list_memories", nil)
	id := firstID(t, list)

	if out, isErr := c.callText(t, "forget", map[string]any{"id": id}); isErr {
		t.Fatalf("forget reported error: %s", out)
	}
	after, _ := c.callText(t, "list_memories", nil)
	if strings.Contains(after, "Temporary note") {
		t.Errorf("memory survived forget; list:\n%s", after)
	}
}

func TestToolErrorsAreResultsNotProtocolErrors(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	// Unknown tool → an isError result (so the host's model can react), never a
	// JSON-RPC protocol error.
	if _, isErr := c.callText(t, "nonexistent_tool", nil); !isErr {
		t.Error("unknown tool should return isError result")
	}
	// Missing required argument → isError result, not a crash.
	if out, isErr := c.callText(t, "remember", map[string]any{"kind": "fact"}); !isErr {
		t.Errorf("remember with empty text should error; got %q", out)
	}
	if out, isErr := c.callText(t, "forget", map[string]any{"id": "not-a-number"}); !isErr {
		t.Errorf("forget with bad id should error; got %q", out)
	}
}

func TestUnknownMethodReturnsProtocolError(t *testing.T) {
	c, _ := startServer(t)
	handshake(t, c)

	// A genuinely unknown *method* (not a tool) is a real JSON-RPC error, so we
	// call the wire directly rather than through req (which fails the test on any
	// error response).
	c.id++
	send(t, c.w, map[string]any{"jsonrpc": "2.0", "id": c.id, "method": "does/notExist"})
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
		if resp.Error == nil || resp.Error.Code != -32601 {
			t.Errorf("unknown method should return -32601, got %+v", resp.Error)
		}
		return
	}
	t.Fatal("no response to unknown method")
}

// firstID pulls the leading "[<n>]" id out of a list_memories rendering.
func firstID(t *testing.T, list string) string {
	t.Helper()
	i := strings.IndexByte(list, '[')
	j := strings.IndexByte(list, ']')
	if i != 0 || j <= i {
		t.Fatalf("could not find id in list:\n%s", list)
	}
	return list[i+1 : j]
}
