// Package brain is a local-first memory and continuity layer for AI agents.
//
// It is the same engine behind the `brain` CLI, the desktop app and the MCP
// server, exposed for embedding directly in your own agent. Memory lives in an
// Obsidian-compatible vault on disk that the user owns; nothing is uploaded.
//
//	b, err := brain.Open("/path/to/vault")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer b.Close()
//
//	// What did the last agent — possibly a different product — leave behind?
//	c, _ := b.Resume("kestrel-one")
//	fmt.Println(c.Render())   // ready to drop into a system prompt
//
//	// Before proposing something, check nobody has already ruled it out.
//	if ruled, _ := b.Tried("switch to a plastic frame", "kestrel-one"); len(ruled) > 0 {
//		fmt.Println(brain.Explain("switch to a plastic frame", ruled))
//	}
//
//	// Record progress as you go, then commit where you stopped.
//	b.Note("kestrel-one", "re-quoted the waveguide; no movement under 10k units")
//	b.Checkpoint(brain.Checkpoint{
//		Project: "kestrel-one",
//		Failed:  []string{"re-quoting the waveguide — no movement under 10k units"},
//		Next:    "quote the display driver alternatives",
//	})
//
// # What this package is, and is not
//
// This is a deliberately small facade over a much larger engine. The
// implementation lives under internal/ and stays there: keeping it private is
// what lets the retrieval, budgeting and consolidation internals change without
// breaking anyone. What is exported here is the surface an agent actually
// needs, which is roughly the same surface the MCP server exposes — that server
// having been the forcing function for working out what an external consumer
// genuinely uses.
//
// The domain types (Memory, Checkpoint, Note, Hit, Ruling, Context) are aliases
// for their internal definitions rather than copies. That makes them part of
// this package's contract: they are stable, and changing them is a breaking
// change. The alternative — parallel structs and a translation layer — buys
// freedom nobody asked for at the cost of conversions in every call.
//
// # Models
//
// Open discovers a local model runtime (Ollama, LM Studio, Jan, Msty) and uses
// it for embeddings. If none is running, everything still works: retrieval
// falls back to lexical and graph traversal, which needs no model at all. Pass
// WithoutEmbedding to skip discovery entirely — useful in tests and CI.
package brain

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pragun/brain/internal/capture"
	"github.com/pragun/brain/internal/contextpack"
	"github.com/pragun/brain/internal/deadend"
	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/mcpserver"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
)

// The domain types. Aliases, not copies — see the package doc.
type (
	// Memory is one durable thing the store knows about the user.
	Memory = memory.Memory
	// Kind classifies a memory: Preference, Person, Fact or Context.
	Kind = memory.Kind
	// Receipt reports what a write actually did — created a fact, or
	// corroborated one already held.
	Receipt = memory.Receipt
	// Checkpoint is where an agent stopped, written well enough that a
	// different agent can start. Failed is the field that earns its keep.
	Checkpoint = session.Checkpoint
	// Note is one line of uncommitted progress.
	Note = session.Note
	// Mention is a checkpoint that touched a file, and what was being worked
	// out at the time. Returned by Why.
	Mention = session.Mention
	// Hit is a retrieved vault note, with its provenance.
	Hit = index.Hit
	// Ruling is something already tried that did not work.
	Ruling = deadend.Ruling
	// Context is everything bearing on a task, budgeted. Call Render for the
	// markdown to put in a model's context window.
	Context = contextpack.Pack
	// SyncReport counts what an Index call changed.
	SyncReport = index.SyncReport
)

// Memory kinds, re-exported so callers need not reach into internal packages.
const (
	Preference = memory.Preference
	Person     = memory.Person
	Fact       = memory.Fact
	Standing   = memory.Context
)

// Brain is an open vault. It is safe to keep for the life of a process and must
// be closed when done. Not safe for concurrent use across goroutines: the
// underlying SQLite handle is single-writer by design.
type Brain struct {
	ix         *index.Index
	embed      *provider.Provider
	embedModel string
	chatModel  string
	rt         *router.Router
	agent      string
}

// An Option configures Open.
type Option func(*config)

type config struct {
	embed     bool
	agent     string
	embedName string
}

// WithoutEmbedding skips model discovery. Retrieval then uses lexical search
// and graph traversal only — no model process required, which is what you want
// in tests and CI.
func WithoutEmbedding() Option { return func(c *config) { c.embed = false } }

// WithEmbeddingModel names the embedding model instead of taking the detected
// default.
func WithEmbeddingModel(name string) Option {
	return func(c *config) { c.embedName = name }
}

// WithAgent sets the name recorded against notes and checkpoints — "claude",
// "cursor", your own product's name. A handoff is meaningless without knowing
// who is handing off, so this is worth setting.
func WithAgent(name string) Option { return func(c *config) { c.agent = name } }

// Open loads the vault at path, creating its cache if absent.
//
// The vault directory itself must exist; brain will not invent one, because
// pointing this at a typo and silently creating an empty knowledge base is a
// worse failure than an error.
func Open(vaultPath string, opts ...Option) (*Brain, error) {
	cfg := config{embed: true, agent: "agent"}
	for _, o := range opts {
		o(&cfg)
	}

	if info, err := os.Stat(vaultPath); err != nil {
		return nil, fmt.Errorf("vault not found at %s: %w", vaultPath, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", vaultPath)
	}

	ix, err := index.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
		func() error { return capture.InitStore(ix.DB) },
	} {
		if err := init(); err != nil {
			ix.Close()
			return nil, err
		}
	}

	b := &Brain{ix: ix, agent: cfg.agent}
	if cfg.embed {
		// A missing runtime is not an error. Everything downstream degrades to
		// lexical and graph retrieval, which is worse but not broken, and a
		// library that refuses to load because Ollama is not running is a
		// library nobody embeds.
		rcfg, err := router.Load(vaultPath)
		if err != nil {
			ix.Close()
			return nil, err
		}
		if rt, err := router.New(rcfg, vaultPath); err == nil {
			b.rt = rt
			b.embed = rt.Local()
			b.embedModel, _ = rt.Model(router.T0)
			b.chatModel, _ = rt.Model(router.T2)
		}
		if cfg.embedName != "" {
			b.embedModel = cfg.embedName
		}
	}
	return b, nil
}

// Close releases the vault.
func (b *Brain) Close() error { return b.ix.Close() }

// Vault is the path this Brain was opened on.
func (b *Brain) Vault() string { return b.ix.Vault }

// Embedded reports whether a model runtime was found. When false, retrieval is
// lexical and graph-only — still useful, measurably weaker.
func (b *Brain) Embedded() bool { return b.embed != nil && b.embedModel != "" }

// --- context: the one an agent should reach for first -----------------------

// A Request describes what you are about to do. Task is the important field:
// "continue the MCP implementation" retrieves differently from a project name
// alone, because it says which corner of the project matters right now.
type Request struct {
	// Task is what you are about to do, in a sentence. Required.
	Task string
	// Project narrows to one piece of work. Optional — when empty, the task
	// text is matched against known projects.
	Project string
	// Budget is the approximate token ceiling. Zero means 4000.
	Budget int
}

// Context assembles everything bearing on a task: where the last agent stopped,
// what it ruled out, uncommitted progress since, the project dossier, the prose
// of relevant notes, notes reached one hop through the user's own links,
// memories with their provenance, and open commitments — spent against a token
// budget and cited by source.
//
// This is the call to make at the start of a task, in preference to Recall.
// Recall answers "what do you know about X"; this answers "give me what I need
// to do X", which is not a longer version of the same question.
func (b *Brain) Context(req Request) (*Context, error) {
	pack, err := contextpack.Build(b.ix, b.embed, b.embedModel, contextpack.Request{
		Task: req.Task, Hint: req.Project, Budget: req.Budget,
	})
	if err != nil {
		return nil, err
	}
	return &pack, nil
}

// Resume picks up a project where the last agent left off. Equivalent to
// Context with a continuation task, and named separately because that is how
// people think about it.
func (b *Brain) Resume(project string) (*Context, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("resume needs a project")
	}
	return b.Context(Request{Task: "resume work on " + project, Project: project})
}

// --- continuity -------------------------------------------------------------

// Note records one line of progress. Cheap and meant to be called often — after
// a decision, a dead end, a surprising discovery. Notes stay uncommitted until
// Checkpoint folds them into a durable record, so use them freely rather than
// saving everything for the end.
func (b *Brain) Note(project, text string) error {
	_, err := session.AddNote(b.ix.DB, project, b.agent, text)
	return err
}

// Notes returns a project's uncommitted progress — work that happened but was
// never written down properly, including anything left by an agent that died
// mid-task.
func (b *Brain) Notes(project string) ([]Note, error) {
	return session.Uncommitted(b.ix.DB, project)
}

// Checkpoint commits where you stopped to a markdown note in the vault and
// returns its slug.
//
// Call it before finishing a session, not after. Anything omitted is lost, and
// the field that matters most is Failed: approaches that did not work are the
// expensive knowledge, and without them the next agent repeats them.
func (b *Brain) Checkpoint(c Checkpoint) (string, error) {
	if c.Agent == "" {
		c.Agent = b.agent
	}
	if err := session.Commit(b.ix.DB, b.ix.Vault, &c); err != nil {
		return "", err
	}
	return c.Slug, nil
}

// History returns a project's checkpoints, newest first.
func (b *Brain) History(project string, n int) ([]Checkpoint, error) {
	return session.History(b.ix.Vault, project, n)
}

// Projects lists the projects that have at least one checkpoint.
func (b *Brain) Projects() ([]string, error) {
	return session.Projects(b.ix.Vault)
}

// --- the intercept ----------------------------------------------------------

// Tried reports whether an approach has already been ruled out, searching every
// dead end recorded across the whole vault — other projects included, and
// findings left in working notes by agents that no longer exist.
//
// Call this before proposing a solution, especially when it seems obvious:
// obvious approaches are the ones already attempted. Pass the project being
// worked on so rulings from elsewhere can be flagged as possibly not
// transferring. An empty result means no record, which is not the same as
// approval.
func (b *Brain) Tried(approach, project string) ([]Ruling, error) {
	return deadend.Check(b.ix.Vault, b.ix.DB, b.embed, b.embedModel, approach, project, 6)
}

// Why reports what was being decided when a file was worked on: the decisions
// taken and the approaches ruled out while it was being touched, newest first.
//
// The complement to `git blame`, which answers who and when and cannot answer
// why. Matching on the path is deliberately loose — an agent records whatever
// path it had in hand and a caller asks with whatever they have — so a bare
// filename or a partial path both resolve.
//
// It reads markdown from the vault, so it needs no model and no index. limit of
// 0 means every match.
func (b *Brain) Why(file string, limit int) ([]Mention, error) {
	return session.Touching(b.ix.Vault, file, limit)
}

// Explain renders the result of Tried as prose to put in front of a model,
// taking the same approach string so the output can quote what was proposed. A
// recorded failure is evidence, not a veto, and the wording says so. An empty
// slice renders as an explicit "no record", which is worth showing: silence and
// approval are different answers.
func Explain(approach string, rulings []Ruling) string {
	return deadend.Render(approach, rulings)
}

// --- memory -----------------------------------------------------------------

// Remember stores something durable about the user and reports what happened:
// whether it created a fact or corroborated one already held. Near-identical
// statements reinforce rather than duplicate.
func (b *Brain) Remember(text string, kind Kind) (Receipt, error) {
	if kind == "" {
		kind = Fact
	}
	return memory.Store(b.ix.DB, b.embed, b.embedModel, &Memory{
		Text: text, Kind: kind, Salience: 0.7, Source: "sdk",
	})
}

// Recall retrieves what is known about the user relevant to a query.
func (b *Brain) Recall(query string, k int) ([]Memory, error) {
	if k <= 0 {
		k = 5
	}
	return memory.Recall(b.ix.DB, b.embed, b.embedModel, query, k)
}

// Memories returns everything currently held.
func (b *Brain) Memories() ([]Memory, error) { return memory.All(b.ix.DB) }

// Forget deletes a memory by id.
func (b *Brain) Forget(id int64) error { return memory.Forget(b.ix.DB, id) }

// --- vault ------------------------------------------------------------------

// Search retrieves vault notes, fusing lexical and vector rankings.
func (b *Brain) Search(query string, k int) ([]Hit, error) {
	if k <= 0 {
		k = 8
	}
	if !b.Embedded() {
		return b.ix.LexicalSearch(query, k)
	}
	return b.ix.HybridSearch(b.embed, b.embedModel, query, k)
}

// Ask retrieves and then answers in prose, citing what it used. Requires a chat
// model; without one it returns an error rather than a guess.
func (b *Brain) Ask(question string, k int) (string, []Hit, error) {
	if b.rt == nil || b.chatModel == "" {
		return "", nil, fmt.Errorf("ask needs a local model runtime; none was found")
	}
	return b.ix.Ask(b.embed, b.embedModel, b.chatModel, question, k, 0)
}

// Index reconciles the vault into the cache: notes, embeddings and memories.
// Call it after writing files into the vault by other means, or on a watcher.
func (b *Brain) Index() (SyncReport, error) {
	rep, err := b.ix.Sync()
	if err != nil {
		return rep, err
	}
	if b.Embedded() {
		if _, err := b.ix.EmbedPending(b.embed, b.embedModel, 32); err != nil {
			return rep, err
		}
	}
	_, err = b.ix.SyncMemories(b.embed, b.embedModel)
	return rep, err
}

// --- serving ----------------------------------------------------------------

// ServeMCP speaks the Model Context Protocol over the given streams, exposing
// this vault's tools to any MCP host. Use it to put your own product in front
// of brain's memory rather than reimplementing the tool surface.
//
// A missing model runtime is not an error, matching Open: retrieval degrades to
// lexical and every continuity tool works untouched. Refusing to serve would
// have contradicted the rest of this API, which is built to keep working on a
// machine with no models on it.
//
// It blocks until the input stream closes.
func (b *Brain) ServeMCP(in io.Reader, out io.Writer) error {
	return mcpserver.New(b.ix.DB, b.rt, b.ix.Vault).Serve(in, out)
}
