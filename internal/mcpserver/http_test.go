package mcpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	_ "modernc.org/sqlite"
)

// The web bridge is the one transport reachable by something other than the
// process that started brain — any tab a user has open can address
// localhost. These tests exercise the two independent gates that stand in for
// the process boundary stdio gets for free: the pairing token and the Origin
// allowlist. Either failing alone must refuse the connection; only both
// passing gets a real tool response.

func newHTTPTestServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	srv := New(db, nil, dir)
	token := "test-token-123"
	allowed := "chrome-extension://abcxyz"

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !validToken(r, token) {
			http.Error(w, "missing or wrong pairing token", http.StatusUnauthorized)
			return
		}
		up := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == allowed },
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go srv.serveConn(conn)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, token, allowed
}

func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/mcp"
}

func TestWebBridgeRejectsAConnectionWithNoPairingToken(t *testing.T) {
	ts, _, allowed := newHTTPTestServer(t)
	hdr := http.Header{"Origin": {allowed}}
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(ts), hdr)
	if err == nil {
		t.Fatal("an unpaired connection must be rejected, not upgraded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 for a missing token, got %+v", resp)
	}
}

func TestWebBridgeRejectsAConnectionFromTheWrongOrigin(t *testing.T) {
	ts, token, _ := newHTTPTestServer(t)
	hdr := http.Header{"Origin": {"https://evil.example.com"}}
	url := wsURL(ts) + "?token=" + token
	_, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err == nil {
		t.Fatal("a connection from an origin not on the allowlist must be rejected")
	}
}

func TestWebBridgeServesARealToolCallWithTheRightTokenAndOrigin(t *testing.T) {
	ts, token, allowed := newHTTPTestServer(t)
	hdr := http.Header{"Origin": {allowed}}
	url := wsURL(ts) + "?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatalf("a correctly paired, correctly originated connection should upgrade: %v", err)
	}
	defer conn.Close()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": protocolVersion},
	}
	b, _ := json.Marshal(req)
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected a response over the socket: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("response was not valid JSON-RPC: %v (%s)", err, msg)
	}
	if resp["error"] != nil {
		t.Errorf("initialize over the web bridge should succeed, got %v", resp["error"])
	}
	if resp["result"] == nil {
		t.Errorf("want a result for initialize, got %v", resp)
	}
}

func TestEnsureLoopbackRejectsNonLoopbackAddresses(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8137": true,
		"localhost:8137": false, // ensureLoopback parses the literal host, not DNS
		"0.0.0.0:8137":   false,
		":8137":          false,
	}
	for addr, want := range cases {
		err := ensureLoopback(addr)
		if (err == nil) != want {
			t.Errorf("ensureLoopback(%q) = %v, want ok=%v", addr, err, want)
		}
	}
}
