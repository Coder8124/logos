// Package mcpserver exposes logos's memory layer as an MCP server.
//
// This is the "memory is the platform" thesis made real: any MCP host — Claude
// Desktop, Claude Code, Cursor, or someone's own application — connects over
// stdio and can build on one local, private memory that follows the user across
// every tool and session. The memory lives in the user's own vault; nothing is
// uploaded. The same store the logos app reads is the store an external agent
// reads and writes, so what you tell one, the others know.
//
// The surface is deliberately more than remember/recall. Beyond reading and
// writing memory, an agent can assemble everything bearing on a task, record
// what it is doing as it works, and commit where it stopped — so a *different*
// agent, in a different application, can resume the same project without the
// user re-explaining anything. That is the point: the AI is replaceable, the
// context is not.
//
// This package is an adapter and nothing more. Argument coercion, dispatch, and
// rendering live here; the judgement about what belongs in a context window
// lives in internal/contextpack, and continuity lives in internal/session.
//
// It speaks MCP over newline-delimited JSON-RPC 2.0 on stdio — the transport
// every MCP host supports.
package mcpserver

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/logos/internal/agentprompt"
	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
)

// protocolVersion is what this server speaks when the host asks for something
// it does not recognise. The supported list is newest first.
//
// This used to be one hard-coded string, and it was the *oldest* revision —
// which quietly cost the server a feature it needs. Tool annotations, the
// protocol's only way to say "this tool just reads", were added in 2025-03-26.
// A host told the session is 2024-11-05 is entitled to ignore them, and a host
// that cannot tell a read from a write has to assume every tool writes. That is
// what makes `resume` — a pure read — unavailable in an editor's read-only
// mode, where recalling where you left off is exactly what you want.
const protocolVersion = "2025-06-18"

// supportedVersions are the revisions this server implements. Nothing in the
// newer ones is mandatory for a tools-only server: the additions they carry
// (elicitation, completions, structured output) are optional capabilities, and
// this server does not advertise them.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// negotiateVersion echoes the client's requested revision when this server
// speaks it, and otherwise answers with the newest one it does — which is what
// the specification asks for, and what lets an old host keep working while a
// current one gets the annotations.
func negotiateVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	for _, v := range supportedVersions {
		if v == p.ProtocolVersion {
			return v
		}
	}
	return protocolVersion
}

// Server holds the memory store and the embedding backend it recalls against.
// embed may be nil — the store then works without vectors (recall ranks by
// keyword), which is what keeps the transport testable without a live
// model.
type Server struct {
	DB *sql.DB
	// vault is needed because checkpoints are markdown files, not rows — see
	// internal/session. Without it the server can read memory but cannot record
	// or recover where an agent left off.
	vault      string
	embed      *provider.Provider
	embedModel string
	// unavailable is why this server cannot serve the vault; see Unavailable.
	unavailable error
	// runtime is the local runtime whether or not it has an embedding model,
	// so SetEmbedModel can turn embedding on after New found none.
	runtime *provider.Provider
	// Shell is how the user reaches this install from a terminal — `logos`,
	// `npx @noeton/logos`, or a full path. Receipts that send the user to a
	// command use it; empty means `logos`.
	Shell string
	// toolSet is the tool set a host is shown and may call; nil is every tool.
	// See SetTools.
	toolSet map[string]bool
}

// Session is one client's connection to the Server: the state that belongs to
// a conversation rather than to the process.
//
// Everything here used to live on Server, which was true enough while stdio was
// the only transport — one process served exactly one client, so process state
// and session state were the same thing. They are not the same thing, and the
// conflation was two latent bugs rather than one simplification:
//
//   - the response encoder was a field, so two clients answered concurrently
//     would interleave frames onto one stream and corrupt both;
//   - the project was resolved once from the launch directory, so any client
//     that did not share that directory would be scoped to it silently, with
//     writes landing in the wrong project rather than failing.
//
// Splitting them costs nothing on stdio — Serve makes one Session and the
// behaviour is identical — and it is the precondition for any transport where
// the client is not the process that started us.
//
// The embedded *Server is deliberate: the shared, immutable half (database,
// vault, embedding backend) promotes through, so only the handful of methods
// that genuinely read session state need to say so in their receiver.
type Session struct {
	*Server

	// project is the work this session defaults to, derived from the roots the
	// client advertised or the folder the host was launched in, rather than
	// from the model remembering to say so. See scope.go. Resolved once, on
	// first use.
	roots           []string
	project         string
	projectInferred bool
	projectOnce     sync.Once

	// hasRoots is whether the host declared the roots capability, and so can be
	// asked which folder is open. See askRoots.
	hasRoots bool

	// clientAgent is who the MCP host said it is at handshake — "claude-code",
	// "cursor", "codex" — read once from initialize and never asked of the
	// model. See identity.go. Empty for a host that omits clientInfo, or before
	// initialize has run.
	clientAgent string

	// startedAt is when Serve began, used at stdin close to tell this session's
	// transcript from one a window closed hours ago left behind. Per session:
	// on Server, a second client connecting moved the first one's start.
	startedAt time.Time

	// served records how many turns of each ingested session this session
	// showed the model, so ingest_distil validates citations against what was
	// rendered rather than against the whole transcript. Session-local by
	// design: it is a fact about this conversation, not about the vault.
	served map[string]int

	// worktree is the linked git worktree the host was launched in, empty in a
	// main checkout. It narrows continuity — sessions and checkpoints — without
	// touching memory, because two worktrees are one repository being worked on
	// in two places. Also see scope.go.
	worktree     string
	worktreeOnce sync.Once

	// lastCheckpoint is what this session last checkpointed. A host that timed
	// the first call out told the model it failed, and the model sends the
	// same checkpoint again; see checkpointRetryWindow.
	lastCheckpoint struct {
		key, slug string
		at        time.Time
	}

	// notes counts progress recorded per project since that project was last
	// checkpointed, nudgedFor holds the projects already warned about, and
	// offered is the project of a nudge composed but not yet known to have
	// reached the host. See nudge.go.
	work      map[string]int
	nudgedFor map[string]bool
	offered   string

	// saved holds the projects this session checkpointed itself, which the
	// close path uses to leave those alone. See unsaved.go.
	saved map[string]bool
}

func checkpointOnDisk(vault, slug string) bool {
	_, err := os.Stat(filepath.Join(vault, filepath.FromSlash(slug)+".md"))
	return err == nil
}

// checkpointRetryWindow is how long an identical checkpoint counts as a retry
// of the last one rather than a new record. A host's tool timeout is well
// inside it; a deliberate second checkpoint with nothing new to say is not
// worth a second file.
var checkpointRetryWindow = 5 * time.Minute

// New builds a server over an open index. rt may be nil: a machine with no
// model runtime still gets every continuity tool, and retrieval falls back to
// lexical. That is the difference between "logos is not much use here" and "the
// MCP server would not start", and a host only ever shows the user the second
// one.
func New(db *sql.DB, rt *router.Router, vault string) *Server {
	if rt == nil {
		return &Server{DB: db, vault: vault}
	}
	srv := &Server{DB: db, vault: vault, runtime: rt.Local().Interactive(embedTimeout)}
	// No embedding model pulled — Ollama with only chat models — serves lexical
	// rather than sending every tool call an embeddings request that can only
	// fail. SetEmbedModel still turns embedding on for a model LOGOS_EMBED names.
	if model, err := rt.Model(router.T0); err == nil {
		srv.embed, srv.embedModel = srv.runtime, model
	}
	return srv
}

// Unavailable is a server that answers the handshake and returns err from
// every tool call. Claude Code caches a plugin server that fails to start and
// skips it for 15 minutes in every session on the machine, so exiting on a
// vault that won't open turned Logos off for a quarter of an hour with the
// cause only in a log. A tool error reaches the model on the next call.
func Unavailable(err error) *Server {
	return &Server{unavailable: err}
}

// embedTimeout bounds each embedding a tool call waits on; see
// provider.Interactive. Long enough for a warm runtime under load, short
// enough that a host waiting on the answer never gives up first.
var embedTimeout = 5 * time.Second

// SetEmbedModel overrides the router's embedding model; "" turns embeddings
// off and retrieval runs lexical. The index is embedded with whatever
// LOGOS_EMBED names, and a query vector from a different model has a different
// length, so every cosine against the stored vectors is 0 — vector retrieval
// dead with nothing to say so.
func (s *Server) SetEmbedModel(model string) {
	if model == "" {
		s.embed, s.embedModel = nil, ""
		return
	}
	s.embed, s.embedModel = s.runtime, model
}

// EmbedModel is the embedding model the server queries with, "" when off.
func (s *Server) EmbedModel() string {
	if s.embed == nil {
		return ""
	}
	return s.embedModel
}

// index wraps the open database as an Index so the context builder can search
// vault prose. The struct is just a vault path and a handle; constructing it
// here avoids a second connection to a single-connection SQLite file.
func (s *Server) index() *index.Index {
	return &index.Index{Vault: s.vault, DB: s.DB}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`

	// Result and Error are set only on the host's answer to a request this
	// server sent, such as roots/list.
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// Serve runs the request loop until stdin closes. Read errors end the loop;
// per-request errors become JSON-RPC error responses so the host stays healthy.
//
// One reader, one writer, one Session, one goroutine: on stdio the connection
// *is* the process, so this is the whole of the concurrency story. The encoder
// is a local rather than a field so that stays true by construction — a second
// caller of Serve gets its own stream and its own session state instead of
// quietly sharing this one's.
func (s *Server) Serve(in io.Reader, w io.Writer) error {
	if s.unavailable == nil {
		if err := memory.Init(s.DB); err != nil {
			return err
		}
	}
	out := json.NewEncoder(w)
	sess := &Session{Server: s, startedAt: time.Now()}
	// A host with no hooks never tells Logos a session ended, so stdin closing
	// is the only notice there is. See recordUnsaved.
	defer sess.recordUnsaved()
	var sendMu sync.Mutex
	send := func(r *response) {
		if r != nil {
			sendMu.Lock()
			out.Encode(r)
			sendMu.Unlock()
		}
	}

	// Requests run one at a time on a worker, in order, so session state needs
	// no locking. The reader stays free: a tool waiting on the model runtime
	// used to hold ping behind it too, and a host that pings to check liveness
	// restarted a server that was only waiting.
	work := make(chan request, 256)
	done := make(chan struct{})

	// A host that gives up on a call sends notifications/cancelled and tells the
	// model the call failed, and the model retries. A call still queued is
	// skipped, since nothing has happened yet; one already running finishes but
	// is not answered, as the protocol asks. pending holds only calls not yet
	// answered, so a late cancel for a finished call is not kept forever.
	var pendingMu sync.Mutex
	pending := map[string]bool{} // request id → cancelled

	// Cursor, Cline and Claude Desktop start the server in / or their own
	// folder and send no roots at initialize, so the folder the user has open
	// is only known by asking. The worker asks and waits for the answer before
	// taking the next request, so a tool call the host sent meanwhile is scoped
	// by it rather than by wherever the server was started. rootsWant is the id
	// of the roots/list request still unanswered; the reader hands its answer
	// over on rootsGot.
	rootsWant := ""
	rootsGot := make(chan []string, 1)
	rootsAsked := 0
	askRoots := func() {
		// An answer that arrived just as an earlier wait timed out is stale.
		select {
		case <-rootsGot:
		default:
		}
		rootsAsked++
		id := "logos-roots-" + strconv.Itoa(rootsAsked)
		pendingMu.Lock()
		rootsWant = `"` + id + `"`
		pendingMu.Unlock()
		sendMu.Lock()
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": "roots/list"})
		sendMu.Unlock()
		select {
		case roots := <-rootsGot:
			sess.setRoots(roots)
		case <-time.After(rootsTimeout):
			pendingMu.Lock()
			rootsWant = ""
			pendingMu.Unlock()
		}
	}

	go func() {
		defer close(done)
		for req := range work {
			if req.Method == "notifications/initialized" || req.Method == "notifications/roots/list_changed" {
				if sess.hasRoots && (req.Method != "notifications/initialized" || len(sess.roots) == 0) {
					askRoots()
				}
				continue
			}
			key := idKey(req.ID)
			pendingMu.Lock()
			cancelled := pending[key]
			pendingMu.Unlock()
			var resp *response
			if !cancelled {
				resp = sess.handle(req)
			}
			pendingMu.Lock()
			cancelled = pending[key]
			delete(pending, key)
			pendingMu.Unlock()
			// The nudge in this response is only spent if the response is
			// actually sent; a cancelled call's reply is dropped unread.
			sess.nudgeSent(!cancelled)
			if !cancelled {
				send(resp)
			}
		}
	}()
	defer func() {
		close(work)
		<-done
	}()

	// A bufio.Scanner gives up permanently on a line longer than its buffer,
	// which ends the session for every later request too. A Reader lets an
	// oversized frame be drained and refused on its own.
	br := bufio.NewReaderSize(in, 64*1024)

	for {
		line, err := readFrame(br)
		if err == errFrameTooLong {
			// The id is unreachable inside a frame we refused to hold, so this
			// cannot be answered in-band. Say so on the transport and carry on;
			// the alternative was silently serving nothing from here onwards.
			send(replyErr(json.RawMessage("null"), -32600,
				"request too large; split the payload or raise the client's limit"))
			continue
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var req request
		if jsonErr := json.Unmarshal([]byte(line), &req); jsonErr != nil {
			// A frame that will not parse still had an id the host is blocking
			// on. Dropping it silently left that host waiting forever, so dig
			// the id out of the raw bytes and answer with the parse error
			// JSON-RPC defines for exactly this.
			send(replyErr(rawID(line), -32700, "parse error: "+jsonErr.Error()))
			continue
		}
		if req.Method == "" && len(req.ID) > 0 && (len(req.Result) > 0 || len(req.Error) > 0) {
			// A response to something this server asked, never a request: a
			// reply to it would be an error for a message the host did not send.
			pendingMu.Lock()
			if rootsWant != "" && idKey(req.ID) == rootsWant {
				rootsWant = ""
				rootsGot <- rootsFromResult(req.Result)
			}
			pendingMu.Unlock()
			continue
		}
		if req.Method == "ping" {
			send(sess.handle(req))
			continue
		}
		if req.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(req.Params, &p) == nil && len(p.RequestID) > 0 {
				key := idKey(p.RequestID)
				pendingMu.Lock()
				if _, ok := pending[key]; ok {
					pending[key] = true
				}
				pendingMu.Unlock()
			}
			continue
		}
		if len(req.ID) > 0 {
			pendingMu.Lock()
			pending[idKey(req.ID)] = false
			pendingMu.Unlock()
		}
		work <- req
	}
}

// idKey spells a request id the same way wherever it appears, so a cancel that
// writes {"requestId": 2} matches the call that wrote "id":2.
func idKey(id json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return string(id)
	}
	return b.String()
}

// maxFrame is the largest request accepted. Generous: a checkpoint carrying a
// long session log or a big context pack is a legitimate payload, and the cost
// of the limit being too low used to be the whole session.
const maxFrame = 32 << 20

var errFrameTooLong = errors.New("frame exceeds the maximum request size")

// readFrame reads one newline-delimited frame. An overlong frame is consumed to
// its newline and reported, so the stream stays aligned and the next request is
// read normally.
func readFrame(br *bufio.Reader) (string, error) {
	var b strings.Builder
	tooLong := false
	for {
		chunk, more, err := br.ReadLine()
		if err != nil {
			if b.Len() > 0 && err == io.EOF {
				break // a final frame with no trailing newline
			}
			return "", err
		}
		if tooLong {
			// Keep draining to the newline so the stream stays aligned, but
			// hold none of it.
		} else if b.Len()+len(chunk) > maxFrame {
			tooLong = true
			b.Reset()
		} else {
			b.Write(chunk)
		}
		if !more {
			break
		}
	}
	if tooLong {
		return "", errFrameTooLong
	}
	return b.String(), nil
}

// rawID recovers the "id" member from a frame too malformed to unmarshal. Best
// effort by design — a frame with no recoverable id gets a null id, which is
// what JSON-RPC says to send when the id cannot be determined.
func rawID(line string) json.RawMessage {
	i := strings.Index(line, `"id"`)
	if i < 0 {
		return json.RawMessage("null")
	}
	rest := strings.TrimSpace(line[i+4:])
	rest, ok := strings.CutPrefix(rest, ":")
	if !ok {
		return json.RawMessage("null")
	}
	rest = strings.TrimSpace(rest)

	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		end = len(rest)
	}
	candidate := strings.TrimSpace(rest[:end])
	if candidate == "" || !json.Valid([]byte(candidate)) {
		return json.RawMessage("null")
	}
	return json.RawMessage(candidate)
}

func (s *Session) handle(req request) (resp *response) {
	// A panic in here used to exit the process, and with it every Logos tool
	// for the rest of the host's session — and the main goroutine's defers
	// never ran, so the session's unsaved work went unrecorded too. One bad
	// argument should cost one call. The stack still goes to stderr, because a
	// failure is reported, never swallowed.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "logos: panic handling %s: %v\n%s\n", req.Method, r, debug.Stack())
			resp = replyErr(req.ID, -32603, fmt.Sprintf("logos failed handling %s: %v", req.Method, r))
		}
	}()

	switch req.Method {
	case "initialize":
		// Roots, when the host sends them, say which folder the user actually
		// has open — better evidence than cwd for a host serving several
		// windows from one process. Captured before the first tool call, which
		// is when the project is resolved.
		s.roots = rootsFromInitialize(req.Params)
		s.hasRoots = clientHasRoots(req.Params)
		s.clientAgent = clientInfoFromInitialize(req.Params)
		return reply(req.ID, map[string]any{
			"protocolVersion": negotiateVersion(req.Params),
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			// The string a host shows the user in its server list.
			"serverInfo": map[string]any{"name": "logos", "version": buildinfo.Version},
			// The protocol's own channel for "here is how to use this server",
			// which conforming hosts put in front of the model with no action
			// from the user. That is the whole reason it lives here rather
			// than in a README nobody wires up: a memory layer the agent has
			// to be told about by hand is one that works only for the person
			// who installed it. See internal/agentprompt.
			"instructions": s.instructions(),
		})
	case "notifications/initialized":
		// notification, no reply
	case "ping":
		return reply(req.ID, map[string]any{})
	case "tools/list":
		return reply(req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		if s.unavailable != nil {
			return reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": s.unavailableText()}},
				"isError": true,
			})
		}
		return s.callTool(req)
	case "resources/list":
		return reply(req.ID, map[string]any{"resources": resourceDefs})
	case "resources/templates/list":
		return reply(req.ID, map[string]any{"resourceTemplates": resourceTemplateDefs})
	case "resources/read":
		if s.unavailable != nil {
			return replyErr(req.ID, -32603, s.unavailableText())
		}
		return s.readResourceCall(req)
	default:
		if len(req.ID) > 0 {
			return replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
	return nil
}

func (s *Session) instructions() string {
	if s.unavailable != nil {
		return s.unavailableText()
	}
	return agentprompt.Text()
}

func (s *Server) unavailableText() string {
	return fmt.Sprintf("Logos is running but can't serve memory: %v. "+
		"Fix that, then reconnect the logos server (/mcp in Claude Code) or start a new session.", s.unavailable)
}

func (s *Session) callTool(req request) *response {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return replyErr(req.ID, -32602, "bad params")
	}
	var args map[string]any
	// A tool call whose arguments are not valid JSON is the host's framing
	// mistake, not the model reasoning to a wrong answer, so it is a protocol
	// error that names the fault rather than a swallowed empty-args dispatch
	// that fails later with a misleading "missing argument".
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return replyErr(req.ID, -32602, "arguments is not valid JSON: "+err.Error())
		}
	}

	if s.toolSet != nil && !s.toolSet[p.Name] {
		return reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": fmt.Sprintf(
				"%s is not in the tool set this server was started with. The user can run it from a terminal with `logos`, or serve every tool with `logos mcp serve --tools all`.", p.Name)}},
			"isError": true,
		})
	}

	// Before dispatch, because every argument helper below returns a zero value
	// for a name it does not find, and the call would otherwise succeed with
	// the argument silently gone.
	if err := validateArgs(p.Name, args); err != nil {
		return reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}

	stalls := s.embed.Stalls()
	text, err := s.dispatch(p.Name, args)
	// Falling back without embeddings is right, and doing it without a word is
	// not: the answer is thinner than usual and the fix — the runtime — is
	// outside Logos (invariant 3).
	if s.embed.Stalls() > stalls {
		note := fmt.Sprintf("\n\n(%s at %s didn't answer embeddings within %s, so this ran without them — "+
			"semantic matching skipped. Retried after %s.)", s.embed.Name, s.embed.BaseURL, embedTimeout, provider.StallCooloff)
		if err != nil {
			err = fmt.Errorf("%w%s", err, note)
		} else {
			text += note
		}
	}
	if err != nil {
		// MCP convention: tool errors are results with isError, not protocol
		// errors, so the model sees them and can react.
		return reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}
	// Only on a result that worked: a failing call is already asking the model
	// to deal with something, and a second thing to attend to competes with it.
	text += s.unsavedNudge()
	return reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

// readResourceCall answers resources/read. Unlike a tool call, an unknown or
// malformed resource is a protocol error rather than an isError result — a
// resource is addressed by a URI the host itself constructed (typically from
// resourceDefs or resourceTemplateDefs), so a bad one is the host's mistake,
// not the model reasoning its way to a wrong answer the way a bad tool
// argument can be.
func (s *Session) readResourceCall(req request) *response {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return replyErr(req.ID, -32602, "bad params")
	}
	text, err := s.readResource(p.URI)
	if err != nil {
		return replyErr(req.ID, -32602, err.Error())
	}
	return reply(req.ID, map[string]any{
		"contents": []map[string]any{{"uri": p.URI, "mimeType": "text/plain", "text": text}},
	})
}

func (s *Session) dispatch(name string, args map[string]any) (string, error) {
	switch name {
	case "remember":
		out, err := s.remember(argStr(args, "text"), argStr(args, "kind"),
			argStr(args, "project"), argBool(args, "global", false))
		// A fact written down is work happening here, and it counts towards the
		// unsaved-work line the same way a note does. Only on success: a
		// refused memory recorded nothing.
		if err == nil {
			s.recordedWork(s.resolveScope(argStr(args, "project")))
		}
		return out, err
	case "recall":
		return s.recall(argStr(args, "query"), argInt(args, "limit", 5),
			argStr(args, "project"), argBool(args, "all_projects", false))
	case "list_memories":
		return s.listMemories(s.resolveProject(""))
	case "forget":
		return s.forget(argStr(args, "id"))
	case "pin_memory":
		return s.pinMemory(argStr(args, "id"), argBool(args, "unpin", false))
	case "exclude_memory":
		return s.excludeMemory(argStr(args, "id"))
	case "context":
		hint, worktree := s.resolveContinuity(argStr(args, "project"))
		out, err := s.context(contextpack.Request{
			Task:     argStr(args, "task"),
			Hint:     hint,
			Worktree: worktree,
			Dir:      s.repoDir(hint),
			Budget:   argInt(args, "budget", 0),
			Since:    contextpack.Since(argStr(args, "since")),
		})
		// A host that started the server in / or the home folder and never said
		// which folder is open leaves no project in scope, and the pack alone
		// reads as nothing being recorded. Say which it is.
		if err == nil && hint == "" {
			out = fmt.Sprintf("No project is in scope: this server was started in %s and the host did not say which folder is open. Call context again and pass `project` to get its handoff and ruled-out approaches.\n\n", scopeDir(s.roots)) + out
		}
		return out, err
	case "resume":
		return s.resume(argStr(args, "project"), argStr(args, "agent"), argInt(args, "budget", 0), contextpack.Since(argStr(args, "since")))
	case "before_you_try":
		// Deliberately not defaulted: before_you_try searches every dead end in
		// the vault on purpose, and the project only labels which rulings came
		// from elsewhere. Scoping it to the current folder would suppress the
		// cross-project warnings that are the whole reason it exists.
		out, err := s.beforeYouTry(argStr(args, "approach"), argStr(args, "project"), s.resolveScope(argStr(args, "project")), scopeDir(s.roots))
		// Counted under the current folder even though the search above is
		// deliberately unscoped: what is being counted is that this agent is
		// about to change something here, which is the point a session starts
		// being worth saving.
		if err == nil {
			s.recordedWork(s.resolveScope(argStr(args, "project")))
		}
		return out, err
	case "why":
		return s.why(argStr(args, "file"), argInt(args, "limit", 5), s.resolveScope(""))
	case "note_progress":
		// A note since the last checkpoint is something the next one carries, so
		// the same arguments again are no longer a retry.
		s.lastCheckpoint.key = ""
		project := s.resolveScope(argStr(args, "project"))
		out, err := s.noteProgress(project, s.agentFor(args), argStr(args, "text"))
		if err == nil {
			s.recordedWork(project)
		}
		return out, err
	case "checkpoint":
		return s.checkpoint(args, "")
	case "handoff":
		return s.checkpoint(args, argStr(args, "to"))
	case "memory_diff":
		return s.memoryDiff(argStr(args, "subject"), argInt(args, "days", 7), s.resolveProject(""))
	case "list_projects":
		return s.listProjectsHere()
	case "ingest_harvest":
		return s.ingestHarvest(argStr(args, "session"), argInt(args, "max_turns", 0))
	case "ingest_distil":
		return s.ingestDistil(argStr(args, "session"), argStr(args, "model"), argStr(args, "next"),
			argList(args, "verified"), argList(args, "failed"), argList(args, "blockers"), argList(args, "decided"))
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// --- json-rpc plumbing ---

// response is one JSON-RPC reply. It is returned rather than written, so the
// caller owns the stream: the stdio loop encodes to stdout under a single
// goroutine, and any future transport encodes to whatever it is answering on.
// A nil *response means there is nothing to send.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func reply(id json.RawMessage, result any) *response {
	if len(id) == 0 {
		return nil // notification: no response
	}
	return &response{JSONRPC: "2.0", ID: id, Result: result}
}

func replyErr(id json.RawMessage, code int, msg string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
