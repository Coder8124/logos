package mcpserver

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"github.com/Coder8124/brain/internal/announce"
	"io"
	"os"
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

// startServer wires a Server (nil embedder, temp SQLite, temp vault) to a
// client over two pipes and runs Serve in the background. Returns the client,
// the DB, and the vault directory — checkpoints are files, so tests need to be
// able to look at them.
func startServer(t *testing.T) (*testClient, *sql.DB, string) {
	t.Helper()
	// These tests are about the protocol, not about scoping, and they name
	// their project explicitly. Pin the worktree axis off so the suite reads
	// the same run from a linked worktree as from the main checkout — see
	// scope.go and scope_test.go, which test that axis on purpose.
	t.Setenv("BRAIN_WORKTREE", "")
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// client → server and server → client.
	toSrv, fromClient := io.Pipe()
	toClient, fromSrv := io.Pipe()
	srv := &Server{DB: db, vault: dir}

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
	return &testClient{t: t, w: fromClient, sc: sc}, db, dir
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
	// The product name, which is what a host displays in its server list.
	if init.ServerInfo.Name != "logos" {
		t.Errorf("serverInfo.name = %q, want logos", init.ServerInfo.Name)
	}
	if init.Capabilities.Tools == nil {
		t.Error("server must advertise a tools capability")
	}
	c.notify("notifications/initialized", nil)
}

func TestHandshakeAndToolDiscovery(t *testing.T) {
	c, _, _ := startServer(t)
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
		// memory: what do you know about X
		"remember": true, "recall": true, "list_memories": true, "forget": true,
		"pin_memory": true, "exclude_memory": true,
		"memory_diff": true, "list_projects": true,
		// continuity: where were we
		"context": true, "resume": true, "note_progress": true,
		"checkpoint": true, "handoff": true,
		// the one that speaks first: has this already been ruled out
		"before_you_try": true,
		// and its counterpart on a file rather than a proposal: what was being
		// decided when this was last touched
		"why": true,
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
	// The diff tool, not quarantine, is under test — a quarantined memory has
	// no EvCreated entry in the timeline, which would fail this for the wrong
	// reason. See quarantine_test.go for the quarantine path itself.
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
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
	// Recall itself is under test, not quarantine — a quarantined memory is
	// invisible to recall by design (see quarantine_test.go for that).
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
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
	// forget is under test, which needs the memory to be listable first — a
	// quarantined memory would not appear in list_memories at all.
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
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

func TestPinMemoryAndExcludeMemoryRoundTrip(t *testing.T) {
	// Pinning is an act on something the user already believes, so this test
	// needs active memories, not proposals: an MCP client's remember is
	// quarantined by default and a quarantined memory is not listable, let
	// alone recallable.
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
	handshake(t, c)

	c.callText(t, "remember", map[string]any{"text": "Always keep this in mind.", "kind": "fact"})
	c.callText(t, "remember", map[string]any{"text": "Never surface this again.", "kind": "fact"})
	list, _ := c.callText(t, "list_memories", nil)
	pinID := idForText(t, list, "Always keep this in mind.")
	excludeID := idForText(t, list, "Never surface this again.")

	if out, isErr := c.callText(t, "pin_memory", map[string]any{"id": pinID}); isErr {
		t.Fatalf("pin_memory reported error: %s", out)
	}
	if out, isErr := c.callText(t, "exclude_memory", map[string]any{"id": excludeID}); isErr {
		t.Fatalf("exclude_memory reported error: %s", out)
	}

	after, _ := c.callText(t, "list_memories", nil)
	if !strings.Contains(after, "[pinned]") {
		t.Errorf("pinned memory should be visibly tagged in list_memories:\n%s", after)
	}
	if !strings.Contains(after, "[excluded]") {
		t.Errorf("excluded memory should be visibly tagged in list_memories:\n%s", after)
	}

	// A pin should survive an irrelevant recall query — that is the entire
	// point of pinning over relying on relevance.
	recalled, _ := c.callText(t, "recall", map[string]any{"query": "something about weather patterns entirely unrelated"})
	if !strings.Contains(recalled, "Always keep this in mind.") {
		t.Errorf("a pinned memory should recall regardless of query relevance, got:\n%s", recalled)
	}
	if strings.Contains(recalled, "Never surface this again.") {
		t.Errorf("an excluded memory must never come back from recall, got:\n%s", recalled)
	}

	// Unpin should return the pinned memory to normal ranking (it stops
	// appearing for an unrelated query) without deleting it.
	if out, isErr := c.callText(t, "pin_memory", map[string]any{"id": pinID, "unpin": true}); isErr {
		t.Fatalf("pin_memory unpin reported error: %s", out)
	}
	after, _ = c.callText(t, "list_memories", nil)
	if strings.Contains(after, "[pinned]") {
		t.Errorf("unpin should clear the pinned tag:\n%s", after)
	}
	if !strings.Contains(after, "Always keep this in mind.") {
		t.Errorf("unpin must not delete the memory:\n%s", after)
	}
}

func idForText(t *testing.T, listing, text string) string {
	t.Helper()
	for _, line := range strings.Split(listing, "\n") {
		if strings.Contains(line, text) {
			start := strings.Index(line, "[")
			end := strings.Index(line, "]")
			if start >= 0 && end > start {
				return line[start+1 : end]
			}
		}
	}
	t.Fatalf("no memory line found containing %q in:\n%s", text, listing)
	return ""
}

func TestToolErrorsAreResultsNotProtocolErrors(t *testing.T) {
	c, _, _ := startServer(t)
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
	c, _, _ := startServer(t)
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

// The handoff, end to end over the wire.
//
// This is the claim the product rests on — the AI can change, the context does
// not — so it is tested at the protocol boundary rather than against the Go
// API. One agent works and hands off; a second agent, which has never seen the
// conversation, asks to resume and receives what it needs to continue.
func TestHandoffBetweenTwoAgents(t *testing.T) {
	c, _, vaultDir := startServer(t)
	handshake(t, c)

	// --- Agent A works. ---
	if _, isErr := c.callText(t, "note_progress", map[string]any{
		"project": "kestrel-one", "agent": "claude",
		"text": "re-quoted the waveguide at volume",
	}); isErr {
		t.Fatal("note_progress failed")
	}

	out, isErr := c.callText(t, "handoff", map[string]any{
		"project": "kestrel-one", "to": "cursor", "agent": "claude",
		"task":   "cut the BOM from $141.20 to the $118 target",
		"failed": []any{"re-quoting the waveguide — no movement under 10k units"},
		"next":   "get a firm quote on the single-mic line",
	})
	if isErr {
		t.Fatalf("handoff failed: %s", out)
	}
	if !strings.Contains(out, "cursor") {
		t.Errorf("handoff should name the recipient: %s", out)
	}

	// The record is a file in the vault, not a row — that is what lets it
	// outlive the index and be read by a human.
	matches, _ := filepath.Glob(filepath.Join(vaultDir, "sessions", "kestrel-one", "*.md"))
	if len(matches) != 1 {
		t.Fatalf("want one checkpoint note in the vault, got %v", matches)
	}

	// --- Agent B arrives cold. ---
	got, isErr := c.callText(t, "resume", map[string]any{
		"project": "kestrel-one", "agent": "cursor",
	})
	if isErr {
		t.Fatalf("resume failed: %s", got)
	}
	for _, want := range []string{
		"cut the BOM",     // what they were doing
		"no movement",     // what was already ruled out — the expensive part
		"single-mic line", // the next step
		"claude",          // who left it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("resume output missing %q — a cold agent cannot continue from this:\n%s", want, got)
		}
	}
}

// A resume with no prior checkpoint must say so. An agent that assumes there
// was one will narrate continuity that never happened.
func TestResumeWithoutACheckpointSaysSo(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	got, isErr := c.callText(t, "resume", map[string]any{"project": "kestrel-one"})
	if isErr {
		t.Fatalf("resume errored: %s", got)
	}
	if !strings.Contains(got, "No checkpoint") {
		t.Errorf("want an explicit no-checkpoint notice, got:\n%s", got)
	}
}

// remember returns a receipt, so the host can tell the user whether it learned
// something new or confirmed something it already had.
// An MCP client's remember is quarantined by default — see quarantineMCP in
// server.go. The receipt has to say so, not claim the fact is already
// remembered when it is really just waiting for `brain review`.
func TestRememberReturnsAReceipt(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)

	first, _ := c.callText(t, "remember", map[string]any{
		"text": "The BOM target is $118.", "kind": "fact",
	})
	if !strings.Contains(first, "queued memory #") {
		t.Errorf("first store from an MCP client should be quarantined, got %q", first)
	}
	second, _ := c.callText(t, "remember", map[string]any{
		"text": "The BOM target is $118.", "kind": "fact",
	})
	if !strings.Contains(second, "already knew that") {
		t.Errorf("restating a fact still pending review should report reinforcement, got %q", second)
	}
}

// BRAIN_TRUST_MCP is the escape hatch for someone who has decided their MCP
// clients do not need a human in the loop — the pre-quarantine behaviour,
// available on purpose rather than lost.
func TestRememberTrustedSkipsQuarantine(t *testing.T) {
	t.Setenv("BRAIN_TRUST_MCP", "1")
	c, _, _ := startServer(t)
	handshake(t, c)

	first, _ := c.callText(t, "remember", map[string]any{
		"text": "The BOM target is $118.", "kind": "fact",
	})
	if !strings.Contains(first, "stored in brain — memory #") {
		t.Errorf("BRAIN_TRUST_MCP should skip quarantine and create directly, got %q", first)
	}
}

// The receipt is a product decision, so it gets a switch. What must not change
// with the switch is the *information*: a user who turns the marker off has
// asked to be told less loudly, not to be told less.
func TestAnnounceLevelChangesTheMarkerNotTheFacts(t *testing.T) {
	for _, tc := range []struct {
		level  string
		marker bool
	}{
		{"on", true},
		{"quiet", false},
		{"off", false},
	} {
		t.Run(tc.level, func(t *testing.T) {
			t.Setenv("LOGOS_ANNOUNCE", tc.level)
			c, _, _ := startServer(t)
			handshake(t, c)
			got, _ := c.callText(t, "remember", map[string]any{
				"text": "The BOM target is $118.", "kind": "fact",
			})
			if strings.Contains(got, announce.Marker) != tc.marker {
				t.Errorf("level %q: marker presence wrong in %q", tc.level, got)
			}
			if !strings.Contains(strings.ToLower(got), "memory #") {
				t.Errorf("level %q dropped the fact itself: %q", tc.level, got)
			}
		})
	}
}

// Models are inconsistent about emitting arrays for list-shaped arguments.
// Losing a checkpoint's hardest-won field to a formatting mismatch would be the
// worst possible failure here, so both shapes are accepted.
func TestCheckpointAcceptsListsAsStrings(t *testing.T) {
	c, _, vaultDir := startServer(t)
	handshake(t, c)

	if _, isErr := c.callText(t, "checkpoint", map[string]any{
		"project": "brain", "task": "wire the MCP tools",
		"failed": "- tried the table-backed store\n- tried caching the parse",
		"next":   "verify against the demo vault",
	}); isErr {
		t.Fatal("checkpoint rejected a string-shaped list")
	}
	matches, _ := filepath.Glob(filepath.Join(vaultDir, "sessions", "brain", "*.md"))
	if len(matches) != 1 {
		t.Fatalf("want one checkpoint, got %v", matches)
	}
	raw, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(raw), "- tried the table-backed store") {
		t.Errorf("string-shaped list was not split into bullets:\n%s", raw)
	}
}
