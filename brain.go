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
// The exported surface is split across files by the question a caller is asking:
// context.go (what do I need to start), continuity.go (record where I stopped),
// deadend.go (has this been ruled out), memory.go (what's known about the user),
// vault.go (search the notes), serve.go (put a host in front of it). This file
// holds the package doc, the domain types, and opening and closing a vault.
package brain

import (
	"fmt"
	"os"

	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/deadend"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/secretary"
	"github.com/Coder8124/brain/internal/session"
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
