// Package mcpserver exposes brain's memory layer as an MCP server.
//
// This is the "memory is the platform" thesis made real: any MCP host — Claude
// Desktop, Claude Code, Cursor, or someone's own application — connects over
// stdio and can build on one local, private memory that follows the user across
// every tool and session. The memory lives in the user's own vault; nothing is
// uploaded. The same store the brain app reads is the store an external agent
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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/pragun/brain/internal/contextpack"
	"github.com/pragun/brain/internal/deadend"
	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/project"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/session"
)

const protocolVersion = "2024-11-05"

// Server holds the memory store and the embedding backend it recalls against.
// embed may be nil — the store then works without vectors (recall falls back to
// salience order), which is what keeps the transport testable without a live
// model.
type Server struct {
	DB *sql.DB
	// vault is needed because checkpoints are markdown files, not rows — see
	// internal/session. Without it the server can read memory but cannot record
	// or recover where an agent left off.
	vault      string
	embed      *provider.Provider
	embedModel string
	out        *json.Encoder
}

func New(db *sql.DB, rt *router.Router, vault string) *Server {
	embed, _ := rt.Model(router.T0)
	return &Server{DB: db, vault: vault, embed: rt.Local(), embedModel: embed}
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
}

// Serve runs the request loop until stdin closes. Read errors end the loop;
// per-request errors become JSON-RPC error responses so the host stays healthy.
func (s *Server) Serve(in io.Reader, w io.Writer) error {
	if err := memory.Init(s.DB); err != nil {
		return err
	}
	s.out = json.NewEncoder(w)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue // unparseable line: ignore, do not crash the transport
		}
		s.handle(req)
	}
	return sc.Err()
}

func (s *Server) handle(req request) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "brain-memory", "version": "0.1.0"},
		})
	case "notifications/initialized":
		// notification, no reply
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefs})
	case "tools/call":
		s.callTool(req)
	default:
		if len(req.ID) > 0 {
			s.replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *Server) callTool(req request) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.replyErr(req.ID, -32602, "bad params")
		return
	}
	var args map[string]any
	json.Unmarshal(p.Arguments, &args)

	text, err := s.dispatch(p.Name, args)
	if err != nil {
		// MCP convention: tool errors are results with isError, not protocol
		// errors, so the model sees them and can react.
		s.reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	s.reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

func (s *Server) dispatch(name string, args map[string]any) (string, error) {
	switch name {
	case "remember":
		return s.remember(argStr(args, "text"), argStr(args, "kind"))
	case "recall":
		return s.recall(argStr(args, "query"), argInt(args, "limit", 5))
	case "list_memories":
		return s.listMemories()
	case "forget":
		return s.forget(argStr(args, "id"))
	case "context":
		return s.context(contextpack.Request{
			Task:   argStr(args, "task"),
			Hint:   argStr(args, "project"),
			Budget: argInt(args, "budget", 0),
		})
	case "resume":
		return s.resume(argStr(args, "project"), argStr(args, "agent"), argInt(args, "budget", 0))
	case "before_you_try":
		return s.beforeYouTry(argStr(args, "approach"), argStr(args, "project"))
	case "note_progress":
		return s.noteProgress(argStr(args, "project"), argStr(args, "agent"), argStr(args, "text"))
	case "checkpoint":
		return s.checkpoint(args, "")
	case "handoff":
		return s.checkpoint(args, argStr(args, "to"))
	case "memory_diff":
		return s.memoryDiff(argStr(args, "subject"), argInt(args, "days", 7))
	case "list_projects":
		return s.listProjects()
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// --- memory operations ---

func (s *Server) remember(text, kindStr string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("remember needs text")
	}
	kind := memory.Fact
	switch memory.Kind(kindStr) {
	case memory.Preference:
		kind = memory.Preference
	case memory.Person:
		kind = memory.Person
	case memory.Context:
		kind = memory.Context
	}
	r, err := memory.Store(s.DB, s.embed, s.embedModel, &memory.Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "mcp",
	})
	if err != nil {
		return "", err
	}
	// A receipt rather than "Remembered." — the host is about to tell the user
	// what happened, and creating a fact is not the same as confirming one it
	// already had.
	switch r.Outcome {
	case memory.EvReinforced:
		return fmt.Sprintf("Already knew that — reinforced memory #%d (%s).", r.Ref, kind), nil
	case memory.EvCreated:
		return fmt.Sprintf("Created memory #%d (%s).", r.ID, kind), nil
	}
	return "Nothing stored.", nil
}

func (s *Server) recall(query string, k int) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("recall needs a query")
	}
	mems, err := memory.Recall(s.DB, s.embed, s.embedModel, query, k)
	if err != nil {
		return "", err
	}
	if len(mems) == 0 {
		return "No relevant memories.", nil
	}
	var b strings.Builder
	for _, m := range mems {
		fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, m.Text)
	}
	return strings.TrimRight(b.String(), "\n"), nil
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
		fmt.Fprintf(&b, "[%d] (%s) %s\n", m.ID, m.Kind, m.Text)
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
	return pack.Render(), nil
}

// --- continuity ---

// resume is context aimed at one question: where did the last agent stop. It is
// the same assembly as context, told to lead with the checkpoint, so an agent
// that has just been handed a project can start with one call.
// beforeYouTry is the one tool here that is not retrieval.
//
// Everything else answers a question the host's model already has. This answers
// one it does not know to ask, which is why the tool description is written as
// an instruction: the model has no way of knowing that the obvious approach it
// is about to suggest was ruled out on a different project by an agent that no
// longer exists.
func (s *Server) beforeYouTry(approach, project string) (string, error) {
	if strings.TrimSpace(approach) == "" {
		return "", fmt.Errorf("before_you_try needs the approach you are considering")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	hits, err := deadend.Check(s.vault, s.DB, s.embed, s.embedModel, approach, project, 6)
	if err != nil {
		return "", err
	}
	return deadend.Render(approach, hits), nil
}

func (s *Server) resume(project, agent string, budget int) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("resume needs a project")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, contextpack.Request{
		Task: "resume work on " + project, Hint: project, Budget: budget,
	})
	if err != nil {
		return "", err
	}
	out := pack.Render()
	if pack.Checkpoint == nil {
		// Say so plainly. An agent that assumes there was a checkpoint and
		// finds none will invent continuity that never existed.
		out += "\n_No checkpoint has been written for this project yet — " +
			"this is context, not a handoff. Call checkpoint before you stop._\n"
	}
	if strings.TrimSpace(agent) != "" && pack.Project != nil {
		session.AddNote(s.DB, pack.Project.Slug, agent, "resumed the project")
	}
	return out, nil
}

func (s *Server) noteProgress(project, agent, text string) (string, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("note_progress needs a project and some text")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	if _, err := session.AddNote(s.DB, project, agent, text); err != nil {
		return "", err
	}
	return "Noted. It stays uncommitted until you call checkpoint.", nil
}

// checkpoint commits the session to the vault. handoffTo is set when the caller
// came in through the handoff tool — same mechanism, stated intent.
func (s *Server) checkpoint(args map[string]any, handoffTo string) (string, error) {
	proj := argStr(args, "project")
	if strings.TrimSpace(proj) == "" {
		return "", fmt.Errorf("checkpoint needs a project")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	c := &session.Checkpoint{
		Project:   proj,
		Agent:     argStr(args, "agent"),
		Task:      argStr(args, "task"),
		State:     argStr(args, "state"),
		Decisions: argList(args, "decisions"),
		Failed:    argList(args, "failed"),
		Questions: argList(args, "questions"),
		Files:     argList(args, "files"),
		Next:      argStr(args, "next"),
		HandoffTo: handoffTo,
	}
	if err := session.Commit(s.DB, s.vault, c); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Checkpoint written to %s.md in the vault.", c.Slug)
	if handoffTo != "" {
		msg += fmt.Sprintf(" Handed off to %s — they can call resume(%q).", handoffTo, c.Project)
	}
	return msg + " Run `brain index` to make it searchable.", nil
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
	for _, e := range res.Added {
		fmt.Fprintf(&b, "+ %s\n", e.Text)
	}
	for _, e := range res.Removed {
		fmt.Fprintf(&b, "- %s\n", e.Text)
	}
	for _, e := range res.Corroborated {
		fmt.Fprintf(&b, "~ %s\n", e.Text)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// listProjects enumerates the projects brain detected, most-recently-active
// first, so a host can navigate the memory by the work it is organised around.
func (s *Server) listProjects() (string, error) {
	ps, err := project.Detect(s.DB)
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		return "No projects detected yet.", nil
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "- %s (last active %s)\n", p.Name, project.Age(p.LastActive))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// --- json-rpc plumbing ---

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // notification: no response
	}
	s.out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) replyErr(id json.RawMessage, code int, msg string) {
	s.out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func argStr(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return v
	}
	return ""
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
