package mcpserver

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/Coder8124/brain/internal/memory"
	"github.com/gorilla/websocket"
)

// http.go: a second transport alongside stdio, for a client that is not the
// process that started us — a browser extension bridging brain into ChatGPT,
// Claude.ai or Perplexity's web UI (see extension/). Session/Server were
// already split for exactly this (see the comment on Session in server.go);
// this file only has to wire a new listener onto sess.handle.
//
// Every request must clear two independent gates before it reaches handle():
//
//  1. a pairing token (pairing.go) proving the caller is the one extension the
//     user configured, not an arbitrary page open in another tab;
//  2. an Origin the server was told to allow — never "*", never a bare
//     https:// page origin, only the extension's own chrome-extension://
//     (or moz-extension://) origin.
//
// Either gate failing is a rejection, not a downgrade. Binding to 127.0.0.1
// keeps this off the network, but the network is not the only attacker who can
// reach localhost — any tab the user has open can, which is what both gates
// are for.

// AllowedOrigins are exact origins permitted to open a WebSocket connection.
// Populated by the caller (cmd/brain/mcp.go) with the extension's real id;
// nil or empty means "accept none", not "accept any" — an explicit opt-in only.
type HTTPConfig struct {
	Addr    string   // e.g. "127.0.0.1:8137" — never 0.0.0.0
	Token   string   // from LoadOrCreateToken
	Origins []string // exact allowed Origin header values
}

// ServeHTTP runs the WebSocket transport until the listener is closed or the
// process exits. One Session per connection, exactly as Serve makes one
// Session per stdio process — a browser extension may hold several tabs open
// at once, each wanting its own project/roots resolution.
func (s *Server) ServeHTTP(cfg HTTPConfig) error {
	if err := ensureLoopback(cfg.Addr); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, o := range cfg.Origins {
		allowed[o] = true
	}
	// CheckOrigin replaces the default (same-origin only, useless for an
	// extension whose origin is chrome-extension://<id>) with the explicit
	// allowlist. Rejecting here, before the handshake completes, is cheaper
	// and more legible than accepting the socket and closing it later.
	up := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return allowed[r.Header.Get("Origin")] },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !validToken(r, cfg.Token) {
			http.Error(w, "missing or wrong pairing token", http.StatusUnauthorized)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already wrote the HTTP error response (wrong Origin, bad
			// handshake); nothing more to do here.
			return
		}
		go s.serveConn(conn)
	})

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}
	return srv.ListenAndServe()
}

// ensureLoopback refuses to start on anything but a loopback address. This
// exists so a typo'd --addr can never turn "local bridge" into "server open to
// the LAN" — CLAUDE.md invariant #5, nothing leaves the machine.
func ensureLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("--addr %q binds every interface; use 127.0.0.1:<port>", addr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--addr %q is not a loopback address; refusing to bind it "+
			"(the web bridge must never be reachable off this machine)", addr)
	}
	return nil
}

// validToken accepts the pairing token as a bearer header or a query param —
// the header for a well-behaved client, the query param because a browser
// extension's WebSocket constructor cannot set arbitrary headers on the
// handshake request.
func validToken(r *http.Request, want string) bool {
	if want == "" {
		return false // never runs unpaired; see cmd/brain/mcp.go
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		if tok, ok := strings.CutPrefix(auth, "Bearer "); ok && tok == want {
			return true
		}
	}
	return r.URL.Query().Get("token") == want
}

// serveConn is ServeHTTP's per-connection loop, the WebSocket analogue of
// Server.Serve's per-process loop: one Session, one JSON-RPC request per
// frame, no framing concerns because WebSocket already delivers whole
// messages.
func (s *Server) serveConn(conn *websocket.Conn) {
	defer conn.Close()
	if err := memory.Init(s.DB); err != nil {
		log.Printf("mcpserver: http session init: %v", err)
		return
	}
	sess := &Session{Server: s}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return // client disconnected or sent a close frame
		}
		var req request
		if jsonErr := json.Unmarshal(msg, &req); jsonErr != nil {
			writeJSON(conn, replyErr(rawID(string(msg)), -32700, "parse error: "+jsonErr.Error()))
			continue
		}
		resp := sess.handle(req)
		if resp != nil {
			writeJSON(conn, resp)
		}
	}
}

func writeJSON(conn *websocket.Conn, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, b)
}
