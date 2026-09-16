// Package engine is the implementation behind github.com/Coder8124/logos.
//
// The root of this module is the published import path and carries the API
// documentation; the code lives here, one directory down, so the repository
// root stays readable. That the root is a thin facade of aliases is the only
// reason this split is possible at all: Go binds an import path to a directory,
// so moving these files without leaving something behind would have changed
// the import every embedder already wrote.
//
// The exported surface is split across files by the question a caller is
// asking: context.go (what do I need to start), continuity.go (record where I
// stopped), deadend.go (has this been ruled out), memory.go (what's known about
// the user), vault.go (search the notes), serve.go (put a host in front of it).
// This file holds the domain types, and opening and closing a vault.
package engine

import (
	"fmt"
	"os"

	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/secretary"
	"github.com/Coder8124/logos/internal/session"
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
	Procedure  = memory.Procedure
)

// Logos is an open vault. It is safe to keep for the life of a process and must
// be closed when done. Not safe for concurrent use across goroutines: the
// underlying SQLite handle is single-writer by design.
type Logos struct {
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
// The vault directory itself must exist; logos will not invent one, because
// pointing this at a typo and silently creating an empty knowledge base is a
// worse failure than an error.
func Open(vaultPath string, opts ...Option) (*Logos, error) {
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

	b := &Logos{ix: ix, agent: cfg.agent}
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
func (b *Logos) Close() error { return b.ix.Close() }

// Vault is the path this Logos was opened on.
func (b *Logos) Vault() string { return b.ix.Vault }

// Embedded reports whether a model runtime was found. When false, retrieval is
// lexical and graph-only — still useful, measurably weaker.
func (b *Logos) Embedded() bool { return b.embed != nil && b.embedModel != "" }
