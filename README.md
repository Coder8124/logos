# brain

**The memory you own.** A local-first second brain that watches your day,
distills it into an Obsidian1 vault you fully control, and lends that memory to
the AI tools you already use — over MCP, on your machine, nothing uploaded
unless you say so.

> **Memory is the product.** Chat assistants forget you the moment a session
> ends. brain is the persistent, private memory that doesn't — the one thing you
> carry between tools, models, and years. Git, but for agentic memory.

Markdown is truth. `.brain/index.db` is a cache you can delete and rebuild. If
this project dies, you keep a vault.

---

## Quickstart

Requires Go 1.26+ and a local model runtime (Ollama, LM Studio, Jan, or Msty).
Endpoints are auto-discovered — no configuration for the common case.

```sh
go build -o bin/brain ./cmd/brain
export BRAIN_VAULT=~/vault          # defaults to ./vault

./bin/brain doctor                  # what runtimes and models it found
./bin/brain index                   # sync the vault into the cache and embed
./bin/brain ask "what did I decide about pricing?"
```

`doctor --probe` actually loads each model and reports which ones honour JSON
schemas — worth running once, since small models that ignore a schema will
quietly poison extraction.

Want something to point it at? `./scripts/seed-demo-vault.sh` builds a synthetic
vault (`~/vaults/kestrel`) with interlocking constraints — a BOM that doesn't
close, a factory missing yield, a schedule with a critical path — so you can ask
hard questions that have findable answers and tell retrieval from autocomplete.

The desktop app is Wails v2:

```sh
cd app && wails dev        # or: wails build
```

---

## Lending the memory to other agents

`brain mcp serve` exposes the memory over the **Model Context Protocol** — the
same protocol Claude Desktop, Claude Code, and Cursor speak, and one any
application can build on. Newline-delimited JSON-RPC 2.0 over stdio.

```json
{
  "mcpServers": {
    "brain": {
      "command": "/absolute/path/to/bin/brain",
      "args": ["mcp", "serve"],
      "env": { "BRAIN_VAULT": "/absolute/path/to/vault" }
    }
  }
}
```

Eleven tools in two families:

**Memory** — *what do you know about X.* `remember` (returns a receipt saying
whether it created a fact or corroborated one it already had), `recall`,
`list_memories`, `forget`, `memory_diff`, `list_projects`.

**Continuity** — *where were we.* `context`, `resume`, `note_progress`,
`checkpoint`, `handoff`.

### The handoff

This is the thesis made mechanical. An agent works, checkpoints, and shuts down;
a different agent — a different *product* — calls `resume` and continues without
anyone re-explaining anything.

```sh
brain note kestrel-one "re-quoted the waveguide; no movement under 10k units"
brain checkpoint kestrel-one --agent claude --next "quote the single-mic line"
brain resume kestrel-one            # from Cursor, from Codex, from anywhere
```

The organising idea is **working tree = SQLite, commits = vault**. A running
agent scribbles working notes into the cache — cheap, frequent, disposable, lost
on rebuild by design. `checkpoint` commits that state to a markdown note in
`sessions/<project>/`, which the indexer then embeds and edges into the graph
for free. Delete `.brain/` and the checkpoints survive, because the file *is* the
record.

The field that earns its keep is **"didn't work."** Anyone can restate the goal;
the expensive knowledge is the three approaches already ruled out, and it is
exactly what dies with a session.

`context(task, project, budget)` assembles everything bearing on a task — the
last checkpoint, uncommitted working notes, the project dossier, the actual prose
of relevant notes, notes reached one hop through your own links, memories with
provenance, and open commitments — spent against a token ceiling, cited by
source, and honest about what it had to leave out.

---

## The command surface

```text
brain ask <q> | search <q> | timeline           query what it knows
brain brief                                     what the secretary thinks you should know now
brain replay [--peek]                           what changed since you were last here
brain reflect | weekly                          stats over your memory; the Sunday review
brain voice | listen | say <text>               talk to it, hear it back (local STT/TTS)
brain name [<name>] | presence [--wake]         the ambient, named assistant
brain jot <thought>                             braindump: capture and auto-file
brain memory [add|forget|log|history|graph|diff] persistent memory and its timeline
brain projects | project <name>                 auto-detected projects and dossiers
brain loop [add|done|drop]                      open commitments
brain graph [focus] [--hops N] [--similar]      the note graph around a note
brain context <task> [--project p] [--budget n] everything bearing on a task, budgeted
brain note <project> <what you did>             record progress; uncommitted until checkpoint
brain checkpoint <project> [--handoff who]      commit where you stopped, into the vault
brain resume <project> | sessions <project>     pick up; read the checkpoint log
brain mcp serve                                 serve the memory to MCP hosts
brain index [--watch] | rollup | review | prune cache sync, proposals, retention
brain capture [--daemon]                        pull episodic events
brain dream [--phase nrem|rem]                  nightly consolidation
brain doctor [--probe] | key set|rm <ref>       runtimes and tiers; API keys
```

Environment: `BRAIN_VAULT` (default `./vault`), `BRAIN_MODEL`, `BRAIN_EMBED`,
`BRAIN_REPOS` (colon-separated repos to mine for commits), `BRAIN_AGENT` (the
name recorded in the session trail, default `cli`).

---

## How it works

Two tiers. **Episodic** memory is SQLite — app focus, URLs, files, commits,
calendar, ~50k rows a day, rolled up and pruned. **Semantic** memory is the vault
— people, projects, topics, routines, ~5 notes a day, permanent.

The pipeline is `events → rollup → proposed notes → review → vault`. Raw events
never become markdown directly; a vault that grows 400 files a day is landfill.
Nothing is written that you haven't accepted, until you raise the auto-accept
threshold yourself.

Retrieval is hybrid: BM25 and vectors fused by reciprocal rank fusion, then
expanded one hop through the graph — because the note your own link says is
relevant is often the one whose words never match your query.

```
vault/
  daily/  people/  projects/  topics/  routines/  sources/
  sessions/<project>/<timestamp>-<agent>.md      # checkpoints
  .brain/                                        # index.db, config — do not sync
```

Model tiers run T0 embeddings (137M) through T2 synthesis (8–24B) locally; T3 is
cloud, BYOK, opt-in, off by default. Extraction uses constrained decoding rather
than tool-calling — small local models are unreliable at tool schemas and
near-perfect with a JSON schema enforced at the sampler.

Measured at **96.0% recall@5 / 99.2% recall@10** on the full 500-question
LongMemEval-S; hybrid beats vector-only by 8.9 points.

---

## Repository

```
cmd/brain/       the CLI — one engine, two front ends
internal/        index, memory, session, contextpack, graph, capture, dream,
                 rollup, secretary, router, voice, mcpserver
app/             Wails v2 desktop app (menubar orb, panel, graph canvas)
docs/            per-subsystem notes: capture, dream, graph, models, presence…
scripts/         demo vault seeding, voice-engine fetch, icon build
```

Further reading: [PRODUCT.md](PRODUCT.md) for the full product surface,
[DESIGN.md](DESIGN.md) for principles and architecture, [CREDITS.md](CREDITS.md)
for prior work that shaped the ideas.
