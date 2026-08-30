# brain

**The memory you own.** A local-first memory and continuity layer for AI agents.
Work with Claude Code, Cursor, Codex or your own agent; when one stops, another
picks up exactly where it left off — over MCP, on your machine, nothing uploaded
unless you say so.

```
Claude Code  ──▶  checkpoint  ──▶  brain  ──▶  resume  ──▶  Cursor
                                (your vault)
```

> **Memory is the product, and continuity is the proof.** Chat assistants forget
> you the moment a session ends. brain doesn't — and the test of that is not
> whether it can find an old note, it is whether an agent that has never seen
> your project can continue one that did.

Markdown is truth. `.brain/index.db` is a cache you can delete and rebuild. If
this project dies, you keep a vault.

---

## Install

```sh
go install github.com/pragun/brain/cmd/brain@latest
brain setup
```

That is the whole thing. `setup` picks a vault (`~/brain` unless you say
otherwise), finds your local model runtime and offers to pull anything missing,
runs the first index, and then **registers brain with every AI agent on the
machine**:

```console
$ brain setup

  vault      /Users/you/brain   (created)
  runtime    Ollama at http://localhost:11434/v1
  embedding  nomic-embed-text ✓
  model      gemma3:4b ✓

  index      0 notes, 0 edges

  hosts
    Claude Code      ✓  registered (claude mcp add --scope user)
    Claude Desktop   ✓  registered (~/Library/Application Support/Claude/…)
    Cursor           ✓  registered (~/.cursor/mcp.json)
    Codex            ✓  registered (codex mcp add)

  Restart the host, then ask it: "what do you remember about me?"
```

Restart the agent and it can reach your memory. `brain setup --vault ~/notes`
points it elsewhere; `brain mcp install` re-runs just the wiring. Both are safe
to run twice — an existing entry is updated, never duplicated, other MCP servers
in those files are left alone, and anything edited gets a `.brain-backup`
beside it.

Where a host ships its own registration command (Claude Code, Codex) brain uses
it, so their config format stays their problem. Only Claude Desktop and Cursor
get hand-written JSON.

A local model runtime — Ollama, LM Studio, Jan, or Msty — is wanted but
optional. Without one, retrieval falls back to lexical search and graph
traversal: measurably weaker, not broken. Endpoints are auto-discovered.

```sh
brain doctor --probe                # which models load, and honour JSON schemas
brain index                         # re-sync after editing the vault by hand
brain ask "what did I decide about pricing?"
```

Want something to point it at? `./scripts/seed-demo-vault.sh` builds a synthetic
vault (`~/vaults/kestrel`) with interlocking constraints — a BOM that doesn't
close, a factory missing yield, a schedule with a critical path — so you can ask
hard questions that have findable answers and tell retrieval from autocomplete.

The desktop app is Wails v2:

```sh
cd app && wails dev        # or: wails build
```

---

## Does it actually work?

Two benchmarks, both reproducible from this repo, both run entirely locally.

**Retrieval.** 96.0% recall@5 / 99.2% recall@10 on the full 500-question
LongMemEval-S. Hybrid beats vector-only by 8.9 points.

**Continuity.** A suite built here, because nothing existing measured handoff:
32 hand-authored scenarios, nine memory systems, one machine, same embedding
model throughout.

```
system                pass  fidelity  carry   leak  signal
brain                81.2%    82.8%   89.1%  33.3%   88.9%
mempalace            46.9%    71.9%   82.8%  58.3%   22.2%
recency-window       46.9%    68.8%   84.4%  83.3%   22.2%
letta                43.8%    67.2%   82.8%  83.3%   22.2%
mem0                 43.8%    67.2%   82.8%  83.3%   22.2%
vector-rag           43.8%    67.2%   82.8%  83.3%   22.2%
none                  0.0%     6.2%    6.2%   0.0%    0.0%
```

Retrieval is not the differentiator — every real system carries 82–89% of the
required facts. The gap is **leakage** (returning the superseded price next to
the current one) and **signal** (saying *this was tried*, *this changed*,
*nothing on record covers that*). A scenario passes only when it clears all
three bars, because a system that returns the right fact *and* the stale one it
replaced has handed the next agent a coin flip.

On the durability family — write, delete every rebuildable artifact, read again
— brain scores **100%** and every other system scores **0%**.

It loses too: arithmetic and recency-conflict are 0% for everyone, and MemPalace
beats brain outright on temporal ordering. Full numbers, method and caveats in
[docs/continuity-benchmark.md](docs/continuity-benchmark.md).

```sh
go run ./cmd/brain bench continuity --brain-only
```

---

## Lending the memory to other agents

`brain mcp serve` exposes the memory over the **Model Context Protocol** — the
same protocol Claude Desktop, Claude Code, and Cursor speak, and one any
application can build on. Newline-delimited JSON-RPC 2.0 over stdio.

`brain setup` writes the config for you. If your host is not one of the four it
knows:

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

Both paths must be absolute. A host launches the server from a directory nobody
chose, so a relative `BRAIN_VAULT` resolves somewhere you will never look — and
brain will appear to work while knowing nothing.

Twelve tools in two families:

**Memory** — *what do you know about X.* `remember` (returns a receipt saying
whether it created a fact or corroborated one it already had), `recall`,
`list_memories`, `forget`, `memory_diff`, `list_projects`.

**Continuity** — *where were we.* `context`, `resume`, `note_progress`,
`checkpoint`, `handoff`, `before_you_try`.

### The intercept

`resume` hands an arriving agent the last checkpoint. That covers the dead end
from yesterday, on the project it is already working on. It does nothing for the
one from March, on a project nobody mentioned, recorded by an agent that no
longer exists — because nobody thinks to search for the thing they are about to
suggest.

`before_you_try` is the other direction. The agent is about to propose
something, and the tool interrupts:

```console
$ brain tried "switch to a plastic frame to save weight"

## This has been tried

One recorded dead end already bears on "switch to a plastic frame to save weight".

- **Switching to a plastic frame — fails the drop test at 1.2m** — tried by
  claude, 3mo ago · `sessions/kestrel-one/20260521-142207-claude`

Before proposing this, say that it has been tried and what happened. If you still
think it is right, say what is different now — a dead end recorded under other
constraints is evidence, not a verdict.
```

It searches every dead end in the vault, across all projects, including findings
left in working notes by an agent that died before it could check in. A recorded
failure is evidence, never a veto, and rulings from a different project are
flagged as possibly not transferring.

The mechanism is a morning's work. The two years of accumulated *we tried that*
is not — which is the part that compounds.

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

## Embedding it in your own agent

MCP is for hosts you don't control. If you're writing the agent yourself, import
the engine directly — same vault, same files, no subprocess and no protocol.

```sh
go get github.com/pragun/brain
```

```go
b, err := brain.Open("/path/to/vault", brain.WithAgent("my-agent"))
if err != nil {
    log.Fatal(err)
}
defer b.Close()

// Starting a task: what did the last agent leave behind?
c, _ := b.Resume("kestrel-one")
fmt.Println(c.Render())        // drop straight into a system prompt

// About to propose something: has it already been ruled out?
approach := "re-quoting the waveguide to bring the BOM down"
if ruled, _ := b.Tried(approach, "kestrel-one"); len(ruled) > 0 {
    fmt.Println(brain.Explain(approach, ruled))
}

// Finishing: commit where you stopped. Failed is the field that earns its keep.
b.Note("kestrel-one", "re-quoted the waveguide; no movement under 10k units")
b.Checkpoint(brain.Checkpoint{
    Project: "kestrel-one",
    Failed:  []string{"re-quoting the waveguide — no movement under 10k units"},
    Next:    "quote the display driver alternatives",
})
```

The rest of the surface is `Context`, `Remember`/`Recall`/`Forget`, `Search`,
`Ask`, `History`, `Projects`, `Index`, and `ServeMCP` if you'd rather serve the
tools than call them. `examples/handoff` is the whole thing end to end: one agent
works and stops, another opens the same vault and continues.

`brain.WithoutEmbedding()` skips model discovery outright, which is what you want
in CI.

Everything else stays under `internal/` on purpose. The exported surface is what
an agent actually uses, so the retrieval, budgeting and consolidation internals
can change without breaking you; the domain types are aliases for their internal
definitions, which makes them part of the contract rather than a copy that drifts.

---

## The command surface

```text
brain setup [--vault DIR] [--yes]               connect brain to every agent here
brain mcp serve | mcp install                   serve the memory; wire up the hosts
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
brain tried <approach> [--project p]            has this already been ruled out?
brain bench continuity [--brain-only]           the handoff suite, against every system installed
brain index [--watch] | rollup | review | prune cache sync, proposals, retention
brain capture [--daemon]                        pull episodic events
brain dream [--phase nrem|rem]                  nightly consolidation
brain doctor [--probe] | key set|rm <ref>       runtimes and tiers; API keys
```

Environment: `BRAIN_VAULT` (default `~/brain`), `BRAIN_MODEL`, `BRAIN_EMBED`,
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
  memories/<kind>.md                             # what it knows about you
  .brain/                                        # index.db, config — do not sync
```

Everything above `.brain/` is the record, including the memories:

```console
$ brain memory add "I prefer terse replies with no preamble"
$ rm -rf $BRAIN_VAULT/.brain && brain index
+1 ~0 -0 =0 · embedded 1 · 1 notes, 0 edges, 2 memories
$ brain memory
  [1] (fact  sal 0.70 · conf █████ 0.90) I prefer terse replies with no preamble
```

Same ids, same confidence. `memories/<kind>.md` is a plain bullet list — edit a
line to correct a fact, delete one to forget it, then reindex.

Model tiers run T0 embeddings (137M) through T2 synthesis (8–24B) locally; T3 is
cloud, BYOK, opt-in, off by default. Extraction uses constrained decoding rather
than tool-calling — small local models are unreliable at tool schemas and
near-perfect with a JSON schema enforced at the sampler.

---

## Repository

```
brain.go         the public API — what an embedding agent imports
examples/        runnable embeddings, starting with the handoff
cmd/brain/       the CLI — one engine, two front ends
internal/        index, memory, session, contextpack, deadend, graph, setup,
                 capture, dream, rollup, secretary, router, voice, mcpserver
chaos/           fault injection: SIGKILL mid-write, full disks, racing processes
app/             Wails v2 desktop app (menubar orb, panel, graph canvas)
bench/           Python adapters for the systems brain is scored against
docs/            the benchmark, plus per-subsystem notes
scripts/         demo vault seeding, voice-engine fetch, icon build
```

Tests run with `go test ./...`. The chaos tier is opt-in and slower:

```sh
go test -tags chaos ./chaos
```

It kills the binary mid-checkpoint forty times, fills a real disk image to
ENOSPC, and races processes on one vault — the failures that only appear when
the kernel is involved.

Further reading: [CREDITS.md](CREDITS.md) for prior work that shaped the ideas,
and `DESIGN.md` / `PRODUCT.md` for principles, architecture and the full product
surface.
