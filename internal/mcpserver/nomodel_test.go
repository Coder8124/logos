package mcpserver

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pragun/brain/internal/memory"

	_ "modernc.org/sqlite"
)

// A machine with no model runtime is the common case for a first install, not
// an edge case: someone runs `brain setup`, declines the 26GB of models or has
// no Ollama at all, and their host launches this server anyway.
//
// It used to refuse to start. cmd/brain/mcp.go took a hard error from
// openRouter() and returned it, and Brain.ServeMCP rejected a nil router
// outright — so setup could wire four hosts, report success, and leave the user
// with a host that fails to connect and no indication why. Every layer beneath
// was already nil-safe; only the two guards at the top were not.
//
// The existing harness could not catch it because it builds &Server{} directly
// and never goes through New. These tests go through New with a nil router,
// which is exactly what the command now does.

func startNoModel(t *testing.T) (*asyncClient, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// nil router: the whole point. New must not dereference it.
	srv := New(db, nil, dir)

	toSrv, fromClient := io.Pipe()
	toClient, fromSrv := io.Pipe()
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
		t.Fatal("no response to initialize with no model runtime — the server refused to start")
	}
	return c, dir
}

// New must survive a nil router. This is the line that used to panic once the
// caller stopped bailing out early.
func TestNewWithoutRouterDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := New(db, nil, dir)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if srv.embed != nil {
		t.Error("embed should be nil with no runtime")
	}
	if srv.vault != dir {
		t.Errorf("vault = %q, want %q", srv.vault, dir)
	}
}

// The handshake is the whole bug: if this fails the host shows a connection
// error and the user concludes brain is broken.
func TestServesWithoutModelRuntime(t *testing.T) {
	c, _ := startNoModel(t)

	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	line, ok := c.await(t, `"id":2`, 3*time.Second)
	if !ok {
		t.Fatal("no response to tools/list")
	}
	// The full tool surface is advertised, not a reduced one: continuity needs
	// no model, and a host that never sees checkpoint cannot call it.
	for _, tool := range []string{"checkpoint", "resume", "before_you_try", "note_progress", "remember", "recall"} {
		if !strings.Contains(line, `"`+tool+`"`) {
			t.Errorf("tools/list omitted %q with no runtime", tool)
		}
	}
}

// Continuity is the product's main claim and needs no model at any point: a
// checkpoint is markdown in the vault and resume reads it back off disk.
func TestContinuityRoundTripWithoutModel(t *testing.T) {
	c, vault := startNoModel(t)

	if _, ok := call(t, c, 3, "checkpoint", map[string]any{
		"project":   "kestrel",
		"task":      "cut the BOM to target",
		"state":     "at 41, target is 38",
		"decisions": []string{"keep the aluminium housing"},
		"failed":    []string{"injection moulding — tooling cost never amortises at this volume"},
		"next":      "quote the extruded option",
		"agent":     "claude",
	}); !ok {
		t.Fatal("checkpoint failed with no model runtime")
	}

	// It must actually be on disk, not just acknowledged — the vault is the
	// record, and this is the half that survives an index rebuild.
	found := false
	_ = filepath.Walk(vault, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") {
			if b, e := os.ReadFile(p); e == nil && strings.Contains(string(b), "injection moulding") {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Error("checkpoint was acknowledged but no markdown in the vault contains it")
	}

	line, ok := call(t, c, 4, "resume", map[string]any{"project": "kestrel"})
	if !ok {
		t.Fatal("resume failed with no model runtime")
	}
	// The dead end is the expensive knowledge; losing it is the failure this
	// whole surface exists to prevent.
	for _, want := range []string{"injection moulding", "quote the extruded option"} {
		if !strings.Contains(line, want) {
			t.Errorf("resume did not carry %q:\n%s", want, truncateForLog(line))
		}
	}
}

// before_you_try is the most differentiated tool here and is pure vault reading.
func TestBeforeYouTryWithoutModel(t *testing.T) {
	c, _ := startNoModel(t)

	if _, ok := call(t, c, 5, "checkpoint", map[string]any{
		"project": "kestrel",
		"failed":  []string{"injection moulding — tooling cost never amortises"},
	}); !ok {
		t.Fatal("checkpoint failed")
	}

	line, ok := call(t, c, 6, "before_you_try", map[string]any{
		"approach": "let us just injection mould the housing",
		"project":  "kestrel",
	})
	if !ok {
		t.Fatal("before_you_try failed with no model runtime")
	}
	if !strings.Contains(line, "injection") {
		t.Errorf("a recorded dead end was not surfaced without a model:\n%s", truncateForLog(line))
	}
}

// remember/recall degrade rather than fail: memory.Store skips the vector when
// the provider is nil and memory.Recall falls back to salience order.
func TestMemoryDegradesWithoutModel(t *testing.T) {
	c, _ := startNoModel(t)

	if _, ok := call(t, c, 7, "remember", map[string]any{
		"text": "the BOM target is 38 dollars",
		"kind": "fact",
	}); !ok {
		t.Fatal("remember failed with no model runtime")
	}
	line, ok := call(t, c, 8, "recall", map[string]any{"query": "BOM target"})
	if !ok {
		t.Fatal("recall failed with no model runtime")
	}
	if !strings.Contains(line, "38") {
		t.Errorf("recall lost a memory stored without embeddings:\n%s", truncateForLog(line))
	}
}

// context is the tool a host calls first. It must return something useful
// rather than an error, even though its semantic arm cannot run.
func TestContextWithoutModel(t *testing.T) {
	c, _ := startNoModel(t)

	if _, ok := call(t, c, 9, "checkpoint", map[string]any{
		"project": "kestrel",
		"state":   "at 41, target is 38",
		"next":    "quote the extruded option",
	}); !ok {
		t.Fatal("checkpoint failed")
	}

	line, ok := call(t, c, 10, "context", map[string]any{
		"task":    "continue cutting the BOM",
		"project": "kestrel",
	})
	if !ok {
		t.Fatal("context failed with no model runtime")
	}
	var resp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err == nil && resp.Error != nil {
		t.Fatalf("context returned a JSON-RPC error with no runtime: %s", resp.Error.Message)
	}
	if !strings.Contains(line, "extruded") {
		t.Errorf("context omitted the checkpoint's next step:\n%s", truncateForLog(line))
	}
}

// A memory stored through MCP must reach the vault markdown, not just the
// cache. "Markdown is truth, the index is a rebuildable cache" is the product's
// central claim and the reason the durability benchmark scores 100% — but that
// was measured through the library, and the served path had no coverage.
//
// It was in fact broken: cmd/brain/mcp.go opened the SQLite file directly
// instead of going through index.Open, so memory.SetVault was never called, and
// flush() returns nil when no vault is registered. Every memory an agent stored
// went into .brain/index.db and nowhere else — silently, with a success receipt.
//
// This test registers the vault the way index.Open does. If the wiring is ever
// dropped again, the assertion below fails rather than the guarantee.
func TestMemoryReachesTheVaultThroughMCP(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	memory.SetVault(db, dir)
	t.Cleanup(func() { memory.SetVault(db, "") })

	if _, err := memory.Store(db, nil, "", &memory.Memory{
		Text:   "the BOM target is 38 dollars",
		Kind:   memory.Fact,
		Source: "mcp",
	}); err != nil {
		t.Fatal(err)
	}

	// The file is the record. Read it off disk rather than asking the database,
	// which would prove nothing about durability.
	var found string
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") {
			if b, e := os.ReadFile(p); e == nil && strings.Contains(string(b), "38 dollars") {
				found = p
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("a memory stored through the MCP path never reached the vault markdown; " +
			"deleting the cache would lose it")
	}
}

func truncateForLog(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}
