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
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/deadend"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/project"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/session"
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
// the client is not the process that started us. See docs/http-transport.md.
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

	// clientAgent is who the MCP host said it is at handshake — "claude-code",
	// "cursor", "codex" — read once from initialize and never asked of the
	// model. See identity.go. Empty for a host that omits clientInfo, or before
	// initialize has run.
	clientAgent string

	// worktree is the linked git worktree the host was launched in, empty in a
	// main checkout. It narrows continuity — sessions and checkpoints — without
	// touching memory, because two worktrees are one repository being worked on
	// in two places. Also see scope.go.
	worktree     string
	worktreeOnce sync.Once
}

// New builds a server over an open index. rt may be nil: a machine with no
// model runtime still gets every continuity tool, and retrieval falls back to
// lexical. That is the difference between "brain is not much use here" and "the
// MCP server would not start", and a host only ever shows the user the second
// one.
func New(db *sql.DB, rt *router.Router, vault string) *Server {
	if rt == nil {
		return &Server{DB: db, vault: vault}
	}
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
//
// One reader, one writer, one Session, one goroutine: on stdio the connection
// *is* the process, so this is the whole of the concurrency story. The encoder
// is a local rather than a field so that stays true by construction — a second
// caller of Serve gets its own stream and its own session state instead of
// quietly sharing this one's.
func (s *Server) Serve(in io.Reader, w io.Writer) error {
	if err := memory.Init(s.DB); err != nil {
		return err
	}
	out := json.NewEncoder(w)
	sess := &Session{Server: s}
	send := func(r *response) {
		if r != nil {
			out.Encode(r)
		}
	}

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
		send(sess.handle(req))
	}
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

func (s *Session) handle(req request) *response {
	switch req.Method {
	case "initialize":
		// Roots, when the host sends them, say which folder the user actually
		// has open — better evidence than cwd for a host serving several
		// windows from one process. Captured before the first tool call, which
		// is when the project is resolved.
		s.roots = rootsFromInitialize(req.Params)
		s.clientAgent = clientInfoFromInitialize(req.Params)
		return reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			// The product name: this is the string a host shows the user in its
			// server list. The repository, the Go module and the binary keep the
			// development name.
			"serverInfo": map[string]any{"name": "logos", "version": "0.1.0"},
		})
	case "notifications/initialized":
		// notification, no reply
	case "ping":
		return reply(req.ID, map[string]any{})
	case "tools/list":
		return reply(req.ID, map[string]any{"tools": toolDefs})
	case "tools/call":
		return s.callTool(req)
	default:
		if len(req.ID) > 0 {
			return replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
	return nil
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
	json.Unmarshal(p.Arguments, &args)

	text, err := s.dispatch(p.Name, args)
	if err != nil {
		// MCP convention: tool errors are results with isError, not protocol
		// errors, so the model sees them and can react.
		return reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}
	return reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

func (s *Session) dispatch(name string, args map[string]any) (string, error) {
	switch name {
	case "remember":
		return s.remember(argStr(args, "text"), argStr(args, "kind"),
			argStr(args, "project"), argBool(args, "global", false))
	case "recall":
		return s.recall(argStr(args, "query"), argInt(args, "limit", 5),
			argStr(args, "project"), argBool(args, "all_projects", false))
	case "list_memories":
		return s.listMemories()
	case "forget":
		return s.forget(argStr(args, "id"))
	case "context":
		hint, worktree := s.resolveContinuity(argStr(args, "project"))
		return s.context(contextpack.Request{
			Task:     argStr(args, "task"),
			Hint:     hint,
			Worktree: worktree,
			Budget:   argInt(args, "budget", 0),
		})
	case "resume":
		return s.resume(argStr(args, "project"), argStr(args, "agent"), argInt(args, "budget", 0))
	case "before_you_try":
		// Deliberately not defaulted: before_you_try searches every dead end in
		// the vault on purpose, and the project only labels which rulings came
		// from elsewhere. Scoping it to the current folder would suppress the
		// cross-project warnings that are the whole reason it exists.
		return s.beforeYouTry(argStr(args, "approach"), argStr(args, "project"))
	case "why":
		return s.why(argStr(args, "file"), argInt(args, "limit", 5))
	case "note_progress":
		return s.noteProgress(s.resolveScope(argStr(args, "project")), argStr(args, "agent"), argStr(args, "text"))
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
	}
	r, err := memory.Store(s.DB, s.embed, s.embedModel, &memory.Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "mcp", Project: project, Agent: s.clientAgent,
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
	// already had.
	switch r.Outcome {
	case memory.EvReinforced:
		return fmt.Sprintf("Already knew that — reinforced memory #%d (%s, %s).", r.Ref, kind, where), nil
	case memory.EvCreated:
		return fmt.Sprintf("Created memory #%d (%s, %s).", r.ID, kind, where), nil
	}
	return "Nothing stored.", nil
}

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
			return fmt.Sprintf("No relevant memories in %s. Pass all_projects to search every project.", project), nil
		}
		return "No relevant memories.", nil
	}
	var b strings.Builder
	for _, m := range mems {
		// Tag anything from outside the current project, so a fact borrowed
		// from elsewhere cannot be read as this project's own settled truth.
		switch {
		case m.Project == "" || m.Project == project:
			fmt.Fprintf(&b, "- (%s) %s\n", m.Kind, m.Text)
		default:
			fmt.Fprintf(&b, "- (%s, from %s) %s\n", m.Kind, m.Project, m.Text)
		}
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
			fmt.Fprintf(&b, "While: %s\n\n", m.Task)
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
		fmt.Fprintf(b, "- %s\n", it)
	}
	b.WriteString("\n")
}

// resume takes the project argument unresolved, because whether it was given at
// all decides whether the worktree narrows it — see resolveContinuity.
func (s *Session) resume(projectArg, agent string, budget int) (string, error) {
	project, worktree := s.resolveContinuity(projectArg)
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("resume needs a project")
	}
	if err := session.Init(s.DB); err != nil {
		return "", err
	}
	pack, err := contextpack.Build(s.index(), s.embed, s.embedModel, contextpack.Request{
		Task: "resume work on " + project, Hint: project, Worktree: worktree, Budget: budget,
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
	// Filed under the scope the pack itself read, so the note lands in the same
	// session a checkpoint will later close — in this worktree, not in the
	// project the worktree belongs to.
	if scope := pack.Continuity(); strings.TrimSpace(agent) != "" && scope != "" {
		session.AddNote(s.DB, scope, agent, "resumed the project")
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
func (s *Session) checkpoint(args map[string]any, handoffTo string) (string, error) {
	proj := s.resolveScope(argStr(args, "project"))
	if strings.TrimSpace(proj) == "" {
		return "", fmt.Errorf("checkpoint needs a project, and none could be inferred from the working directory")
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
		Verified:  argList(args, "verified"),
		Blockers:  argList(args, "blockers"),
		Commands:  argList(args, "commands"),
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
