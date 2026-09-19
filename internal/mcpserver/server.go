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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/logos/internal/agentprompt"
	"github.com/Coder8124/logos/internal/announce"
	"github.com/Coder8124/logos/internal/buildinfo"
	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/project"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/untrusted"
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
	// startedAt is when Serve began, used at stdin close to tell this session's
	// transcript from one a window closed hours ago left behind.
	startedAt time.Time
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
	roots       []string
	project     string
	projectOnce sync.Once

	// hasRoots is whether the host declared the roots capability, and so can be
	// asked which folder is open. See askRoots.
	hasRoots bool

	// clientAgent is who the MCP host said it is at handshake — "claude-code",
	// "cursor", "codex" — read once from initialize and never asked of the
	// model. See identity.go. Empty for a host that omits clientInfo, or before
	// initialize has run.
	clientAgent string

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
	s.startedAt = time.Now()
	out := json.NewEncoder(w)
	sess := &Session{Server: s}
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
		return reply(req.ID, map[string]any{"tools": toolDefs})
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
		return s.listMemories()
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
		out, err := s.beforeYouTry(argStr(args, "approach"), argStr(args, "project"))
		// Counted under the current folder even though the search above is
		// deliberately unscoped: what is being counted is that this agent is
		// about to change something here, which is the point a session starts
		// being worth saving.
		if err == nil {
			s.recordedWork(s.resolveScope(argStr(args, "project")))
		}
		return out, err
	case "why":
		return s.why(argStr(args, "file"), argInt(args, "limit", 5))
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
		return s.memoryDiff(argStr(args, "subject"), argInt(args, "days", 7))
	case "list_projects":
		return s.listProjectsHere()
	case "ingest_harvest":
		return s.ingestHarvest(argStr(args, "session"), argInt(args, "max_turns", 0))
	case "ingest_distil":
		return s.ingestDistil(argStr(args, "session"), argStr(args, "model"), argStr(args, "next"),
			argList(args, "verified"), argList(args, "failed"), argList(args, "blockers"))
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// --- memory operations ---

// remember stores a fact scoped to the project the session is working on.
// global=true opts out, for the things that really do apply everywhere — a
// standing preference about how the user likes replies is not a fact about
// this repository.
func (s *Session) remember(text, kindStr, projectArg string, global bool) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("remember needs text")
	}
	project := ""
	if !global {
		project = s.resolveProject(projectArg)
	}
	kind := memory.Fact
	switch memory.Kind(kindStr) {
	case memory.Preference:
		kind = memory.Preference
	case memory.Person:
		kind = memory.Person
	case memory.Context:
		kind = memory.Context
	case memory.Procedure:
		kind = memory.Procedure
	}
	// A procedure earns its slot by naming what goes wrong without it — the
	// trap test. Refuse here, with the reason, rather than storing a
	// convention that will never be flagged as one again: a rejected write
	// must not come back looking like a stored one.
	if kind == memory.Procedure {
		if err := procedure.Validate(procedure.ParseRecord(text)); err != nil {
			return "", err
		}
	}
	r, err := memory.Store(s.DB, s.embed, s.embedModel, &memory.Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "mcp", Project: project, Agent: s.clientAgent,
		Quarantined:       reviewEverythingMCP(),
		ReviewIfContested: !trustMCP(),
	})
	if err != nil {
		return "", err
	}
	// Name the scope in the receipt. The host shows this to the user, and
	// "which pile did that go in" is the one thing they cannot otherwise see.
	where := "everywhere"
	if project != "" {
		where = project
	}
	// A receipt rather than "Remembered." — the host is about to tell the user
	// what happened, and creating a fact is not the same as confirming one it
	// already had, or queuing one that still needs a yes.
	switch r.Outcome {
	case memory.EvReinforced:
		if r.StillQueued {
			return s.receipt(fmt.Sprintf("still queued — memory #%d (%s, %s) is waiting for review; the user runs `%s review` to accept or reject it", r.Ref, kind, where, s.shell())), nil
		}
		return s.receipt(fmt.Sprintf("already knew that — reinforced memory #%d (%s, %s)", r.Ref, kind, where)), nil
	case memory.EvQuarantined:
		return s.receipt(s.quarantineReceipt(r.ID, string(kind), where, r)), nil
	case memory.EvCreated:
		return s.receipt(fmt.Sprintf("stored in logos — memory #%d (%s, %s)", r.ID, kind, where)), nil
	}
	return "Nothing stored.", nil
}

// trustMCP and reviewEverythingMCP are the two ends of how much scrutiny a
// `remember` from an MCP client gets before it counts as known.
//
// The middle — the default — is that a write goes active unless it contradicts
// something already stored, and only the contradiction waits for a person. See
// the Review-only-what-is-in-dispute comment in memory.Store for why.
//
// This used to quarantine everything, on the reasoning that an MCP client is a
// different process and the user is not necessarily watching when it writes.
// The reasoning was right about the risk and wrong about the remedy, and the
// old comment here said so without following it: an agent whose every write
// silently queues has lost its memory just as thoroughly as one that writes
// with no oversight at all. MCP is not one path among several — it is the only
// path an agent has, so "review everything" meant nothing an agent learned ever
// reached the next agent unless the user personally typed `logos review`. What
// people install this for is continuity. A queue that has to be drained by hand
// before continuity happens is a bill most users will simply not pay, and the
// facts sit unreviewed while both agents behave as though nothing was stored.
//
// Both escape hatches stay, because the old default was right for someone:
//
//	LOGOS_TRUST_MCP=1    never queue, not even a contradiction
//	LOGOS_REVIEW_ALL=1   queue every agent write, as before
//
// LOGOS_* environment variables rather than a config file, matching every other
// one-bit decision in this codebase.
func trustMCP() bool { return os.Getenv("LOGOS_TRUST_MCP") != "" }

func reviewEverythingMCP() bool { return os.Getenv("LOGOS_REVIEW_ALL") != "" }

// recall searches this project's memories plus the global ones. allProjects
// widens it to everything, which is the "unless explicitly asked" half — an
// agent that genuinely wants another project's history can have it, but has to
// say so rather than getting it by accident.
func (s *Session) recall(query string, k int, projectArg string, allProjects bool) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("recall needs a query")
	}
	var (
		mems []memory.Memory
		err  error
	)
	project := ""
	if !allProjects {
		project = s.resolveProject(projectArg)
	}
	if project == "" {
		mems, err = memory.Recall(s.DB, s.embed, s.embedModel, query, k)
	} else {
		mems, err = memory.RecallInProject(s.DB, s.embed, s.embedModel, query, project, k)
	}
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		if project != "" {
			// A typo and a real project with nothing on the subject used to get
			// the same sentence, and the agent draws the same conclusion from
			// it: this work has no recorded facts, carry on without them. Only
			// one of those is true. resolveProject accepts any string, so the
			// check has to be here.
			if !s.projectExists(project) {
				return fmt.Sprintf("No project named %s in this vault%s", untrusted.Inline(project), s.knownProjectsSentence()) + s.awaitingReview(), nil
			}
			return fmt.Sprintf("No relevant memories in %s. Pass all_projects to search every project.", project) + s.awaitingReview(), nil
		}
		return "No relevant memories." + s.awaitingReview(), nil
	}
	var b strings.Builder
	for _, m := range mems {
		// Tag anything from outside the current project, so a fact borrowed
		// from elsewhere cannot be read as this project's own settled truth.
		switch {
		// Inline, because each memory is one bullet and a stored fact may contain
		// anything: a newline plus "## Where we left off" turned a recalled fact
		// into a section of logos's own frame, with a "Next step" the reading
		// agent had no way to tell from the real one.
		case m.Project == "" || m.Project == project:
			fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, untrusted.Inline(m.Text))
		default:
			fmt.Fprintf(&b, "- (%s, from %s) %s\n", m.Kind, m.Project, untrusted.Inline(m.Text))
		}
	}
	return strings.TrimRight(b.String(), "\n") + s.awaitingReview(), nil
}

func (s *Server) listMemories() (string, error) {
	mems, err := memory.All(s.DB)
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		return "No memories yet.", nil
	}
	var b strings.Builder
	for _, m := range mems {
		tag := ""
		// Pin state has to be visible here too, not just in the CLI — a host's
		// model deciding whether to pin/exclude something needs to see what
		// already is, or it will keep re-pinning the same memory every session.
		switch m.Pin {
		case memory.PinAlways:
			tag = " [pinned]"
		case memory.PinNever:
			tag = " [excluded]"
		}
		fmt.Fprintf(&b, "[%d] (%s)%s %s\n", m.ID, m.Kind, tag, untrusted.Inline(m.Text))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (s *Server) forget(idStr string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("forget needs a numeric memory id")
	}
	if err := memory.Forget(s.DB, id); err != nil {
		return "", err
	}
	return "Forgotten.", nil
}

// pinMemory sets or clears always-include. unpin covers both directions of
// override (see memory.Unpin) so a host does not need a third tool just to
// walk back an exclude_memory call.
func (s *Server) pinMemory(idStr string, unpin bool) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("pin_memory needs a numeric memory id")
	}
	if unpin {
		if err := memory.Unpin(s.DB, id); err != nil {
			return "", err
		}
		return "Unpinned — back to normal ranking.", nil
	}
	if err := memory.Pin(s.DB, id); err != nil {
		return "", err
	}
	return "Pinned — always included in context packs, budget permitting.", nil
}

func (s *Server) excludeMemory(idStr string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return "", fmt.Errorf("exclude_memory needs a numeric memory id")
	}
	if err := memory.Exclude(s.DB, id); err != nil {
		return "", err
	}
	return "Excluded — kept on record, never surfaced.", nil
}

// --- memory-layer operations: the surface other applications build on ---

// contextPack assembles everything relevant to a file, project, or topic — the
// project dossier, standing preferences, and related memories — as one markdown
// bundle a host can drop straight into its model's context.
func (s *Server) context(req contextpack.Request) (string, error) {
	if strings.TrimSpace(req.Task) == "" && strings.TrimSpace(req.Hint) == "" {
		return "", fmt.Errorf("context needs a task (what you are trying to do) or a project")
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, req)
	if err != nil {
		return "", err
	}
	return s.lead(pack) + pack.Render() + s.awaitingReview(), nil
}

// lead puts the receipt above the pack rather than below it. A person skimming
// a tool result reads the first line and stops; a summary underneath a page of
// markdown is a summary nobody sees.
func (s *Server) lead(pack contextpack.Pack) string {
	carried := pack.Carried()
	if carried == "" {
		return ""
	}
	r := announce.Say(s.vault, "recalled "+carried)
	if r == "" {
		return ""
	}
	return r + "\n\n"
}

// --- continuity ---

// resume is context aimed at one question: where did the last agent stop. It is
// the same assembly as context, told to lead with the checkpoint, so an agent
// that has just been handed a project can start with one call.
// beforeYouTry is the one tool here that is not retrieval.
//
// Everything else answers a question the host's model already has. This answers
// two it does not know to ask: whether the approach was already ruled out, and
// whether there is a known-good way to do it with a trap the obvious way falls
// into. Which is why the tool description is written as an instruction: the
// model has no way of knowing either on its own.
func (s *Server) beforeYouTry(approach, project string) (string, error) {
	if strings.TrimSpace(approach) == "" {
		return "", fmt.Errorf("before_you_try needs the approach you are considering")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	hits, semanticErr, err := deadend.CheckNoting(s.vault, s.DB, s.embed, s.embedModel, approach, project, 6)
	if err != nil {
		return "", err
	}
	// The corpus is gathered unranked and unfiltered (p=nil, so RecallProcedures
	// takes its All()-backed fallback with no reinforcement side effect) — Check
	// does its own lexical-plus-semantic scoring below, and a candidate the
	// embedding pass here dropped early is exactly the one the lexical arm is
	// for. Mirrors deadend's own Collect-then-Check split, and for the same
	// reason: ranking degrades to lexical-only with no embedder, gathering must
	// not have degraded it already.
	corpus, err := memory.RecallProcedures(s.DB, nil, "", "", 0)
	if err != nil {
		return "", err
	}
	procHits, err := procedure.Check(corpus, s.embed, s.embedModel, approach, project, 4)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(untrusted.Boundary)
	b.WriteString("\n\n")
	b.WriteString(deadend.Render(approach, hits))
	b.WriteString(deadend.SemanticSkipped(semanticErr))
	if section := procedure.Render(procHits); section != "" {
		b.WriteString("\n")
		b.WriteString(section)
	}
	return b.String(), nil
}

// why reports what was being decided when a file was worked on.
//
// Reads markdown out of the vault and needs no model and no index, so it works
// on a machine with neither — which matters, because the moment it is useful is
// the moment an agent is about to change something it does not understand.
func (s *Server) why(file string, limit int) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("why needs a file path")
	}
	if s.vault == "" {
		return "", fmt.Errorf("why reads checkpoints from the vault, and no vault is configured")
	}
	mentions, err := session.Touching(s.vault, file, limit)
	if err != nil {
		return "", err
	}
	if len(mentions) == 0 {
		// Distinguish the two nothings. "Nothing was recorded" is a fact about
		// the record; "there is no reason" is a claim about the code, and this
		// tool is not entitled to make it.
		return fmt.Sprintf(
			"No checkpoint mentions %s.\n\nNothing was written down while this file was worked on, or it was "+
				"recorded under a different path. Do not read this as evidence the code is arbitrary.", file), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# What was decided around %s\n\n", file)
	for _, m := range mentions {
		when := "an unknown date"
		if m.TS > 0 {
			when = time.Unix(m.TS, 0).Format("2 Jan 2006")
		}
		who := m.Agent
		if who == "" {
			who = "an unrecorded author"
		}
		fmt.Fprintf(&b, "## %s — %s", when, who)
		if m.Project != "" {
			fmt.Fprintf(&b, " · %s", m.Project)
		}
		b.WriteString("\n\n")
		if m.Task != "" {
			// "While:" is a label, so its value is one line. A task recorded with
			// a heading in it could otherwise close the label and open a section
			// of its own, directly above the evidence why exists to present.
			fmt.Fprintf(&b, "While: %s\n\n", untrusted.Inline(m.Task))
		}
		// Ruled out first: a decision explains the shape of the code, and a dead
		// end explains why it is not some other shape — which is what someone
		// about to "fix" it needs.
		writeList(&b, "Ruled out", m.Failed)
		writeList(&b, "Decided", m.Decisions)
		writeList(&b, "Still open", m.Questions)
		fmt.Fprintf(&b, "Source: %s\n\n", m.Slug)
	}
	b.WriteString("This is what was recorded while the file was touched, not an analysis of " +
		"the code. Treat it as evidence about intent, and check it still holds.\n")
	return b.String(), nil
}

func writeList(b *strings.Builder, label string, items []string) {
	var kept []string
	for _, it := range items {
		if s := strings.TrimSpace(it); s != "" {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:**\n", label)
	for _, it := range kept {
		// Every item here is a checkpoint field somebody else's agent wrote. One
		// bullet, one line — a newline in a recorded dead end was enough to end
		// the list and start a heading of logos's own.
		fmt.Fprintf(b, "- %s\n", untrusted.Inline(it))
	}
	b.WriteString("\n")
}

// resume takes the project argument unresolved, because whether it was given at
// all decides whether the worktree narrows it — see resolveContinuity.
func (s *Session) resume(projectArg, agent string, budget int, since contextpack.Since) (string, error) {
	project, worktree := s.resolveContinuity(projectArg)
	chose := ""
	if strings.TrimSpace(project) == "" {
		// Hosts without hooks (Cursor, Codex, Claude Desktop) launched outside
		// any repository give no project, and an error sent the user off to
		// learn the name another tool filed the work under. The most recent
		// checkpoint is the likeliest thing they mean by "resume"; saying which
		// was picked lets the agent correct course if it was not. The worktree
		// is dropped because it was read from where the host stands, not from
		// the project being resumed.
		ps := s.checkpointedProjects()
		if len(ps) == 0 {
			return "", fmt.Errorf("resume needs a project%s", s.knownProjects())
		}
		project, worktree = ps[0].name, ""
		chose = fmt.Sprintf("_No project given and none to tell from where this host was launched — resuming %s, the most recently checkpointed project%s. Pass project to resume a different one._\n\n",
			untrusted.Inline(project), s.knownProjects())
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, contextpack.Request{
		Task: "resume work on " + project, Hint: project, Worktree: worktree, Dir: s.repoDir(project),
		Agent: s.agentFor(map[string]any{"agent": agent}), Budget: budget, Since: since,
	})
	if err != nil {
		return "", err
	}
	out := chose + s.lead(pack) + pack.Render()
	if pack.Checkpoint == nil {
		// Say so plainly. An agent that assumes there was a checkpoint and
		// finds none will invent continuity that never existed.
		out += "\n_No checkpoint has been written for this project yet — " +
			"this is context, not a handoff. Call checkpoint before you stop._\n"
	}
	out += s.awaitingReview()
	// Filed under the scope the pack itself read, so the note lands in the same
	// session a checkpoint will later close — in this worktree, not in the
	// project the worktree belongs to.
	if scope := pack.Continuity(); strings.TrimSpace(agent) != "" && scope != "" {
		session.AddNote(s.DB, scope, agent, "resumed the project")
	}
	return out, nil
}

func (s *Server) noteProgress(project, agent, text string) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("note_progress needs a project and some text%s", s.knownProjects())
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("note_progress needs a project and some text")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	if _, err := session.AddNote(s.DB, project, agent, text); err != nil {
		return "", err
	}
	return s.receipt("noted in logos — uncommitted until you checkpoint"), nil
}

// receipt marks a line as ours so the person watching the transcript can find
// it without reading it. See internal/announce for why this is a setting and
// not a constant.
//
// It lives on Server rather than Session because the tools that write are split
// across both, and a receipt that appeared on half of them would be worse than
// none: an inconsistent marker teaches people the absence of a marker means
// nothing happened.
func (s *Server) receipt(what string) string {
	if r := announce.Say(s.vault, what); r != "" {
		return r
	}
	// At LOGOS_ANNOUNCE=off the model still needs to know what happened, even
	// though the user has asked not to be told about it. Silence towards the
	// user is not silence towards the caller.
	return upperFirst(what)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// agentFor names who wrote a checkpoint or note: the model's own agent
// argument when it gave one, otherwise the host's handshake name. Without the
// fallback an agent that skipped the optional argument was filed as "agent",
// and a handoff could not say which host stopped there.
func (s *Session) agentFor(args map[string]any) string {
	if a := argStr(args, "agent"); a != "" {
		// "claude" under claude-code is the host's name cut short, not another
		// agent; recording it as typed split one session's trail in two.
		if s.clientAgent != "" && strings.HasPrefix(s.clientAgent, strings.ToLower(a)+"-") {
			return s.clientAgent
		}
		return a
	}
	return s.clientAgent
}

// checkpoint commits the session to the vault. handoffTo is set when the caller
// came in through the handoff tool — same mechanism, stated intent.
func (s *Session) checkpoint(args map[string]any, handoffTo string) (string, error) {
	proj := s.resolveScope(argStr(args, "project"))
	if strings.TrimSpace(proj) == "" {
		return "", fmt.Errorf("checkpoint needs a project, and none could be inferred from the working directory%s", s.knownProjects())
	}
	key, _ := json.Marshal([]any{proj, handoffTo, args})
	// The file is checked too: a receipt for a checkpoint that is no longer on
	// disk would be a success-shaped failure.
	if last := s.lastCheckpoint; last.key == string(key) && time.Since(last.at) < checkpointRetryWindow &&
		checkpointOnDisk(s.vault, last.slug) {
		msg := s.receipt(fmt.Sprintf("checkpoint already saved to logos — %s.md; this identical retry was not written again", last.slug))
		if handoffTo != "" {
			msg += fmt.Sprintf(" Handed off to %s — they can call resume(%q).", handoffTo, proj)
		}
		return msg, nil
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	c := &session.Checkpoint{
		Project:   proj,
		Agent:     s.agentFor(args),
		Task:      argStr(args, "task"),
		State:     argStr(args, "state"),
		Decisions: argList(args, "decisions"),
		Failed:    argList(args, "failed"),
		Verified:  argList(args, "verified"),
		Blockers:  argList(args, "blockers"),
		Commands:  argList(args, "commands"),
		Questions: argList(args, "questions"),
		Files:     argList(args, "files"),
		Next:      argStr(args, "next"),
		HandoffTo: handoffTo,
	}
	var dropped int
	c.Failed, dropped = session.DropPlaceholders(c.Failed)
	if err := session.Commit(s.DB, s.vault, c); err != nil {
		return "", err
	}
	s.lastCheckpoint.key, s.lastCheckpoint.slug, s.lastCheckpoint.at = string(key), c.Slug, time.Now()
	s.checkpointed(c.Project)
	msg := s.receipt(fmt.Sprintf("checkpoint saved to logos — %s.md", c.Slug))
	if dropped > 0 {
		msg += fmt.Sprintf(" Dropped %d placeholder %s from failed; leave failed empty when nothing was ruled out.",
			dropped, map[bool]string{true: "entry", false: "entries"}[dropped == 1])
	}
	if session.NextReadsAsMoreThanOneStep(c.Next) {
		msg += " Recorded as given; `next` reads as more than one step — the parts that are conditional or later usually belong in `questions`, which resume prints as \"Still open\"."
	}
	if handoffTo != "" {
		msg += fmt.Sprintf(" Handed off to %s — they can call resume(%q).", handoffTo, c.Project)
	}
	// No "run `logos index`": resume and before_you_try read the checkpoint off
	// disk, so it is usable the moment this returns. See cmd/logos/session.go.
	return msg, nil
}

// memoryDiff reports what the memory learned, dropped, or corroborated over the
// last `days`, optionally about one subject. Instant and offline — it reads the
// append-only memory log, no model.
func (s *Server) memoryDiff(subject string, days int) (string, error) {
	if days <= 0 {
		days = 7
	}
	until := time.Now()
	since := until.AddDate(0, 0, -days)
	res, err := memory.Diff(s.DB, subject, since.Unix(), until.Unix())
	if err != nil {
		return "", err
	}
	if res.Empty() {
		return "Nothing changed in that window.", nil
	}
	var b strings.Builder
	// One line per entry, for the same reason recall collapses: the +/-/~ marker
	// is the only thing distinguishing logos's reading of the window from the
	// stored text beside it.
	for _, e := range res.Added {
		fmt.Fprintf(&b, "+ %s\n", untrusted.Inline(e.Text))
	}
	for _, e := range res.Removed {
		fmt.Fprintf(&b, "- %s\n", untrusted.Inline(e.Text))
	}
	for _, e := range res.Corroborated {
		fmt.Fprintf(&b, "~ %s\n", untrusted.Inline(e.Text))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// knownProjects ends a refusal for a missing project with the projects that
// have checkpoints, newest first. A model that does not know the name has no
// other way to learn it from the refusal, and a host that launches the server
// in / never supplies one.
func (s *Server) knownProjects() string {
	ps := s.checkpointedProjects()
	if len(ps) == 0 {
		return ""
	}
	const maxKnown = 5
	if len(ps) > maxKnown {
		ps = ps[:maxKnown]
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = fmt.Sprintf("%s (%s)", untrusted.Inline(p.name), project.Age(p.ts))
		if p.agent != "" {
			parts[i] = fmt.Sprintf("%s (%s, %s)", untrusted.Inline(p.name), project.Age(p.ts), untrusted.Inline(p.agent))
		}
	}
	return ". Known projects: " + strings.Join(parts, ", ")
}

// projectExists reports whether the vault has ever heard this exact name —
// either a memory filed under it or a session directory carrying it. Both are
// consulted because a project can have checkpoints and no memory, or memory
// and no checkpoint, and either one makes the name real.
func (s *Server) projectExists(name string) bool {
	if ok, err := memory.HasProject(s.DB, name); err == nil && ok {
		return true
	}
	names, err := session.Scopes(s.vault)
	if err != nil {
		return false
	}
	for _, n := range names {
		// Either direction counts: a worktree scope is "shop/fix-auth" while
		// the enumerator lists "shop", so a name can be the parent of a known
		// scope or a scope under a known parent.
		if n == name || strings.HasPrefix(n, name+"/") || strings.HasPrefix(name, n+"/") {
			return true
		}
	}
	return false
}

// knownProjectsSentence is knownProjects punctuated as an answer rather than
// as the tail of a refusal.
func (s *Server) knownProjectsSentence() string {
	if known := s.knownProjects(); known != "" {
		return known + "."
	}
	return "."
}

type knownProject struct {
	name, agent string
	ts          int64
}

// checkpointedProjects lists the projects that have a checkpoint, most recent
// first.
func (s *Server) checkpointedProjects() []knownProject {
	// Scopes, not Projects: a scope this returns is one an agent will pass
	// straight back to resume, and a worktree scope was the single thing none
	// of these surfaces could name.
	names, err := session.Scopes(s.vault)
	if err != nil {
		return nil
	}
	var ps []knownProject
	for _, n := range names {
		if h, err := session.History(s.vault, n, 1); err == nil && len(h) > 0 {
			ps = append(ps, knownProject{n, h[0].Agent, h[0].TS})
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ts > ps[j].ts })
	return ps
}

// scopeCount is a scope and how much work is filed under it.
type scopeCount struct {
	name string
	n    int
}

// checkpointedScopes lists the scopes holding at least one checkpoint, with
// the count. Separate from checkpointedProjects because that one carries the
// most recent checkpoint's agent and timestamp and this one only needs a
// number; both drop the scopes holding none, which is the part that matters.
func (s *Server) checkpointedScopes() []scopeCount {
	names, err := session.Scopes(s.vault)
	if err != nil {
		return nil
	}
	var out []scopeCount
	for _, n := range names {
		h, err := session.History(s.vault, n, 0)
		if err != nil || len(h) == 0 {
			continue
		}
		out = append(out, scopeCount{n, len(h)})
	}
	return out
}

// listProjectsHere is listProjects with the one fact it could never supply:
// where the caller is standing.
//
// listProjects is a method on Server, and its line in the tool switch was the
// only one that threaded no session state — so the single tool whose answer is
// a list of names had no way to mark the name belonging to the agent asking.
// An agent handed four names fans out and calls resume once per name; three of
// those answers are somebody else's work. The scope comes from the same
// observed sources every other continuity tool uses, never from an argument.
//
// The resource surface keeps the unscoped listing: logos://projects is a
// directory of the vault, not advice to an agent standing somewhere.
func (s *Session) listProjectsHere() (string, error) {
	body, err := s.listProjects()
	if err != nil {
		return "", err
	}
	here := s.resolveScope("")
	if here == "" {
		return body, nil
	}
	head := fmt.Sprintf("You are in %s", untrusted.Inline(here))
	if h, err := session.History(s.vault, here, 0); err == nil && len(h) > 0 {
		word := "checkpoints"
		if len(h) == 1 {
			word = "checkpoint"
		}
		head += fmt.Sprintf(" (%d %s)", len(h), word)
	} else {
		head += " (no checkpoints yet)"
	}
	return head + ".\n\nEverything in this vault:\n" + body, nil
}

// listProjects enumerates the projects logos detected, most-recently-active
// first, so a host can navigate the memory by the work it is organised around.
func (s *Server) listProjects() (string, error) {
	ps, err := project.Detect(s.DB)
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		// The activity rollup is not where checkpoints live. A model looking
		// for a name to resume was told there were none while sessions/ held
		// them, and reported an empty memory. `logos projects` falls back the
		// same way.
		// Only scopes that actually hold a checkpoint. This branch used to print
		// every session directory under a heading asserting they all had one,
		// contradicting itself on the rows reading "(0 checkpoints)" — and those
		// empty rows are the ghost projects a host leaves behind in any folder
		// it was opened in, so the list was advertising its own exhaust.
		if ps := s.checkpointedScopes(); len(ps) > 0 {
			var b strings.Builder
			// A statement, not an instruction. "call resume with one" was the
			// only line in this server aimed at the model, and it sat directly
			// above a list — which a thorough agent reads as "enumerate these",
			// and did: four resume calls where one was wanted.
			b.WriteString("No activity rollup yet. These scopes hold checkpoints:\n")
			for _, p := range ps {
				word := "checkpoints"
				if p.n == 1 {
					word = "checkpoint"
				}
				fmt.Fprintf(&b, "- %s (%d %s)\n", p.name, p.n, word)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}
		return "No projects detected yet.", nil
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "- %s (last active %s)\n", p.Name, project.Age(p.LastActive))
	}
	return strings.TrimRight(b.String(), "\n"), nil
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

func argStr(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return v
	}
	return ""
}

// argBool accepts a real bool or the string a model emits when it is being
// loose about JSON types, which is often enough to matter on a flag that
// changes which memories come back.
func argBool(args map[string]any, k string, def bool) bool {
	switch v := args[k].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func argInt(args map[string]any, k string, def int) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// argList accepts either a JSON array or a newline/semicolon separated string.
// Hosts vary in how reliably their models emit arrays for list-shaped
// arguments, and rejecting a checkpoint because the decisions arrived as a
// string would lose the work it was recording.
func argList(args map[string]any, k string) []string {
	var out []string
	switch v := args[k].(type) {
	case []any:
		for _, it := range v {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case []string:
		out = v
	case string:
		for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ';' }) {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*+ "))
			if line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// quarantineReceipt names the review command this install answers to. Under
// npx or the plugin alone there is no logos on PATH, and "run `logos review`"
// left the memory queued behind a command the user could not run.
func (s *Server) quarantineReceipt(id int64, kind, where string, r memory.Receipt) string {
	// Why it queued, not just that it did. A memory only waits for review when
	// it disputes one already stored, so the receipt quotes the memory in
	// dispute — that is what lets the agent raise it in the conversation the
	// user is already having, rather than leaving it for a queue they open
	// some other day.
	if r.Contested != 0 {
		return fmt.Sprintf("queued memory #%d (%s, %s) — it contradicts memory #%d, %q. The user runs `%s review` to settle which is current; until then neither answer changes",
			id, kind, where, r.Contested, r.ContestedText, s.shell())
	}
	return fmt.Sprintf("queued memory #%d (%s, %s) for review — the user runs `%s review` to accept or reject it before it becomes active", id, kind, where, s.shell())
}

// shell is the command this install answers to; see quarantineReceipt.
func (s *Server) shell() string {
	if s.Shell == "" {
		return "logos"
	}
	return s.Shell
}

// awaitingReview is the line every read appends while the review queue is not
// empty. Quarantine keeps an agent's memories out of recall until the user says
// yes, and a user who is never told there is anything to say yes to leaves them
// there for good — while the agent reads the empty recall as the fact never
// having been stored. A failed count is said, not swallowed, but does not fail
// the read it is attached to.
func (s *Server) awaitingReview() string {
	n, err := memory.PendingCount(s.DB)
	if err != nil {
		return fmt.Sprintf("\n\n(could not count the memories waiting for review: %v)", err)
	}
	if n == 0 {
		return ""
	}
	if n == 1 {
		return fmt.Sprintf("\n\n1 memory is waiting for your review — `%s review`", s.shell())
	}
	return fmt.Sprintf("\n\n%d memories are waiting for your review — `%s review`", n, s.shell())
}
