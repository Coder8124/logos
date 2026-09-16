// Package logos is a local-first memory and continuity layer for AI agents.
//
// It is the same engine behind the `logos` CLI, the desktop app and the MCP
// server, exposed for embedding directly in your own agent. Memory lives in an
// Obsidian-compatible vault on disk that the user owns; nothing is uploaded.
//
//	b, err := logos.Open("/path/to/vault")
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
//		fmt.Println(logos.Explain("switch to a plastic frame", ruled))
//	}
//
//	// Record progress as you go, then commit where you stopped.
//	b.Note("kestrel-one", "re-quoted the waveguide; no movement under 10k units")
//	b.Checkpoint(logos.Checkpoint{
//		Project: "kestrel-one",
//		Failed:  []string{"re-quoting the waveguide — no movement under 10k units"},
//		Next:    "quote the display driver alternatives",
//	})
//
// # What this package is, and is not
//
// This is a deliberately small facade over a much larger engine. The
// implementation lives in [github.com/Coder8124/logos/engine] and under
// internal/, and stays there: keeping it private is what lets the retrieval,
// budgeting and consolidation internals change without breaking anyone. What is
// exported here is the surface an agent actually needs, which is roughly the
// same surface the MCP server exposes — that server having been the forcing
// function for working out what an external consumer genuinely uses.
//
// The declarations here are aliases for the engine package's, not copies, so
// the two cannot drift: [Logos] is the same type either way and its methods are
// documented on the engine package. This file exists because Go binds an import
// path to a directory, and this is the import path that was published. The code
// moved down a level to keep the repository root readable; the import did not
// move, and will not.
//
// The domain types (Memory, Checkpoint, Note, Hit, Ruling, Context) are in turn
// aliases for their internal definitions rather than copies. That makes them
// part of this package's contract: they are stable, and changing them is a
// breaking change. The alternative — parallel structs and a translation layer —
// buys freedom nobody asked for at the cost of conversions in every call.
//
// # Models
//
// Open discovers a local model runtime (Ollama, LM Studio, Jan, Msty) and uses
// it for embeddings. If none is running, everything still works: retrieval
// falls back to lexical and graph traversal, which needs no model at all. Pass
// WithoutEmbedding to skip discovery entirely — useful in tests and CI.
package logos

import "github.com/Coder8124/logos/engine"

// The published surface. Aliases, so an embedder and the engine can never hold
// two different versions of the same type.
type (
	// Logos is an open vault. It is safe to keep for the life of a process and
	// must be closed when done. Not safe for concurrent use across goroutines:
	// the underlying SQLite handle is single-writer by design. Its methods are
	// documented on [engine.Logos].
	Logos = engine.Logos
	// An Option configures Open.
	Option = engine.Option
	// A Request describes what you are about to do, for Context.
	Request = engine.Request
	// Memory is one durable thing the store knows about the user.
	Memory = engine.Memory
	// Kind classifies a memory: Preference, Person, Fact or Context.
	Kind = engine.Kind
	// Receipt reports what a write actually did — created a fact, or
	// corroborated one already held.
	Receipt = engine.Receipt
	// Checkpoint is where an agent stopped, written well enough that a
	// different agent can start. Failed is the field that earns its keep.
	Checkpoint = engine.Checkpoint
	// Note is one line of uncommitted progress.
	Note = engine.Note
	// Mention is a checkpoint that touched a file, and what was being worked
	// out at the time. Returned by Why.
	Mention = engine.Mention
	// Hit is a retrieved vault note, with its provenance.
	Hit = engine.Hit
	// Ruling is something already tried that did not work.
	Ruling = engine.Ruling
	// Context is everything bearing on a task, budgeted. Call Render for the
	// markdown to put in a model's context window.
	Context = engine.Context
	// SyncReport counts what an Index call changed.
	SyncReport = engine.SyncReport
)

// Memory kinds, re-exported so callers need not reach into internal packages.
const (
	Preference = engine.Preference
	Person     = engine.Person
	Fact       = engine.Fact
	Standing   = engine.Standing
	Procedure  = engine.Procedure
)

// Open loads the vault at path, creating its cache if absent.
//
// The vault directory itself must exist; logos will not invent one, because
// pointing this at a typo and silently creating an empty knowledge base is a
// worse failure than an error.
func Open(vaultPath string, opts ...Option) (*Logos, error) {
	return engine.Open(vaultPath, opts...)
}

// WithoutEmbedding skips model discovery. Retrieval then uses lexical search
// and graph traversal only — no model process required, which is what you want
// in tests and CI.
func WithoutEmbedding() Option { return engine.WithoutEmbedding() }

// WithEmbeddingModel names the embedding model instead of taking the detected
// default.
func WithEmbeddingModel(name string) Option { return engine.WithEmbeddingModel(name) }

// WithAgent sets the name recorded against notes and checkpoints — "claude",
// "cursor", your own product's name. A handoff is meaningless without knowing
// who is handing off, so this is worth setting.
func WithAgent(name string) Option { return engine.WithAgent(name) }

// Explain renders the result of Tried as prose to put in front of a model,
// taking the same approach string so the output can quote what was proposed. A
// recorded failure is evidence, not a veto, and the wording says so. An empty
// slice renders as an explicit "no record", which is worth showing: silence and
// approval are different answers.
func Explain(approach string, rulings []Ruling) string { return engine.Explain(approach, rulings) }
