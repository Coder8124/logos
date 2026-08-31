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

// The MCP server's input is a pipe from someone else's process. Everything here
// is a frame a real host can send, by bug or by accident: a truncated write, a
// checkpoint larger than the read buffer, a tool called with the wrong types.
//
// The bar is that the server answers every request it can, refuses the ones it
// cannot as an in-band error, and never leaves the host waiting forever.
//
// These use their own harness rather than testClient: a synchronous
// request/response client over an io.Pipe deadlocks the moment the server
// replies to something the client is not currently waiting for, which is
// exactly the situation being tested. Here a reader goroutine drains the server
// continuously and every assertion has a timeout.

type asyncClient struct {
	w     io.WriteCloser
	lines chan string
}

func startAsync(t *testing.T) (*asyncClient, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	toSrv, fromClient := io.Pipe()
	toClient, fromSrv := io.Pipe()
	srv := &Server{DB: db, vault: dir}

	served := make(chan error, 1)
	go func() { served <- srv.Serve(toSrv, fromSrv) }()

	c := &asyncClient{w: fromClient, lines: make(chan string, 256)}
	go func() {
		sc := bufio.NewScanner(toClient)
		sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()

	t.Cleanup(func() {
		fromClient.Close()
		select {
		case <-served:
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down after stdin close")
		}
	})

	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if _, ok := c.await(t, `"id":1`, 3*time.Second); !ok {
		t.Fatal("no response to initialize")
	}
	return c, dir
}

func (c *asyncClient) send(t *testing.T, line string) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := c.w.Write([]byte(line + "\n"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server stopped reading; a write blocked for 5s")
	}
}

// await waits for a line containing needle. Lines that do not match are
// discarded, which is what a real host does with responses it is not waiting on.
func (c *asyncClient) await(t *testing.T, needle string, d time.Duration) (string, bool) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return "", false
			}
			if strings.Contains(line, needle) {
				return line, true
			}
		case <-deadline:
			return "", false
		}
	}
}

func call(t *testing.T, c *asyncClient, id int, tool string, args map[string]any) (string, bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.send(t, string(body))
	return c.await(t, `"id":`+itoa(id), 10*time.Second)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// A malformed frame is dropped silently (server.go:97, "unparseable line:
// ignore"). The transport survives — but a host that sent a request with an id
// and is blocking on its response waits forever. JSON-RPC defines -32700 for
// exactly this.
func TestMalformedFrameIsAnswered(t *testing.T) {
	c, _ := startAsync(t)

	c.send(t, `{"jsonrpc":"2.0","id":999,"method":"tools/call","params":{`)
	if _, ok := c.await(t, `"id":999`, 2*time.Second); !ok {
		t.Error("a request whose frame failed to parse got no response at all; " +
			"a host blocking on that id waits until the process is killed " +
			"(JSON-RPC -32700 is the defined answer)")
	}

	// Whatever the answer, the transport must still work.
	c.send(t, `{"jsonrpc":"2.0","id":1000,"method":"ping","params":{}}`)
	if _, ok := c.await(t, `"id":1000`, 3*time.Second); !ok {
		t.Fatal("the server stopped answering after a malformed frame")
	}
}

func TestGarbageInputSurvives(t *testing.T) {
	c, _ := startAsync(t)

	for _, line := range []string{
		`not json at all`,
		`[]`,
		`null`,
		`{"jsonrpc":"2.0"}`,
		`{"jsonrpc":"2.0","id":1,"method":""}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":null}`,
		"\x00\x01\xff",
		strings.Repeat(" ", 1000),
	} {
		c.send(t, line)
	}

	c.send(t, `{"jsonrpc":"2.0","id":2000,"method":"ping","params":{}}`)
	if _, ok := c.await(t, `"id":2000`, 5*time.Second); !ok {
		t.Error("the server died on garbage input")
	}
}

// A frame larger than the 4MB read buffer ends the scan loop, which ends Serve.
// One oversized checkpoint therefore kills the session for every later request.
func TestOversizeFrameDoesNotKillTheServer(t *testing.T) {
	c, _ := startAsync(t)

	huge, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 500, "method": "tools/call",
		"params": map[string]any{
			"name": "checkpoint",
			"arguments": map[string]any{
				"project": "kestrel", "state": strings.Repeat("x", 5<<20),
			},
		},
	})
	c.send(t, string(huge))

	c.send(t, `{"jsonrpc":"2.0","id":501,"method":"ping","params":{}}`)
	if _, ok := c.await(t, `"id":501`, 5*time.Second); !ok {
		t.Error("a 5MB frame ended the session: every later request from this host " +
			"goes unanswered and the host has to restart the transport. A large " +
			"checkpoint or a long context pack is not an unusual payload")
	}
}

// Wrong types, missing arguments, unknown tools: all must come back as in-band
// tool errors, never as a dead transport.
func TestHostileToolArguments(t *testing.T) {
	c, _ := startAsync(t)

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"unknown tool", "no_such_tool", map[string]any{}},
		{"no args at all", "remember", nil},
		{"wrong type for text", "remember", map[string]any{"text": 42}},
		{"wrong type for budget", "context", map[string]any{"task": "x", "budget": "lots"}},
		{"null argument", "recall", map[string]any{"query": nil}},
		{"array where string expected", "resume", map[string]any{"project": []any{1, 2}}},
		{"object where string expected", "note_progress", map[string]any{"project": map[string]any{}, "text": "x"}},
		{"empty strings", "checkpoint", map[string]any{"project": "", "state": ""}},
		{"huge query", "recall", map[string]any{"query": strings.Repeat("waveguide ", 50000)}},
		{"nul bytes", "remember", map[string]any{"text": "a\x00b"}},
		{"emoji only", "recall", map[string]any{"query": "🚀🚀🚀"}},
		{"negative k", "recall", map[string]any{"query": "x", "k": -5}},
		{"absurd k", "recall", map[string]any{"query": "x", "k": 1 << 40}},
		{"path escape", "checkpoint", map[string]any{"project": "../../escape", "state": "x"}},
		{"non-latin project", "checkpoint", map[string]any{"project": "プロジェクト", "state": "x"}},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, ok := call(t, c, 100+i, tc.tool, tc.args)
			if !ok {
				t.Errorf("no response to %s(%s) within 10s — the host hangs", tc.tool, tc.name)
				return
			}
			t.Logf("%.160s", line)
		})
	}

	c.send(t, `{"jsonrpc":"2.0","id":3000,"method":"tools/list","params":{}}`)
	if _, ok := c.await(t, `"id":3000`, 5*time.Second); !ok {
		t.Error("the server was left unhealthy by hostile arguments")
	}
}

// The README documents twelve tools by name. Discovery must match the docs, or
// the docs are wrong for anyone wiring this up.
func TestToolListMatchesTheDocumentedSurface(t *testing.T) {
	c, _ := startAsync(t)

	c.send(t, `{"jsonrpc":"2.0","id":4000,"method":"tools/list","params":{}}`)
	line, ok := c.await(t, `"id":4000`, 5*time.Second)
	if !ok {
		t.Fatal("no response to tools/list")
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Type string `json:"type"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatal(err)
	}

	documented := map[string]bool{
		"remember": true, "recall": true, "list_memories": true, "forget": true,
		"memory_diff": true, "list_projects": true, "context": true, "resume": true,
		"note_progress": true, "checkpoint": true, "handoff": true, "before_you_try": true,
		"why": true,
	}
	got := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		got[tool.Name] = true
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description; the description is the prompt the host's model reads", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("%s has input schema type %q, want object", tool.Name, tool.InputSchema.Type)
		}
	}
	for name := range documented {
		if !got[name] {
			t.Errorf("README documents %q but tools/list does not offer it", name)
		}
	}
	for name := range got {
		if !documented[name] {
			t.Errorf("tools/list offers %q, which the README does not document", name)
		}
	}
}

// Past the limit the frame cannot be held, so it cannot be answered by id. It
// must still be refused on the transport and the session must continue.
func TestFrameOverTheHardLimitIsRefusedNotFatal(t *testing.T) {
	c, _ := startAsync(t)

	over, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 600, "method": "tools/call",
		"params": map[string]any{
			"name":      "checkpoint",
			"arguments": map[string]any{"project": "kestrel", "state": strings.Repeat("x", maxFrame+(1<<20))},
		},
	})
	c.send(t, string(over))

	if line, ok := c.await(t, `-32600`, 10*time.Second); !ok {
		t.Error("an over-limit frame was not refused on the transport")
	} else {
		t.Logf("refused: %.120s", line)
	}

	c.send(t, `{"jsonrpc":"2.0","id":601,"method":"ping","params":{}}`)
	if _, ok := c.await(t, `"id":601`, 10*time.Second); !ok {
		t.Error("the session did not survive an over-limit frame")
	}
}
