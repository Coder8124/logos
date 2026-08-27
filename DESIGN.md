# brain — design

A local-first memory and continuity layer for AI agents. It observes what you
do, distils it into an Obsidian vault you own, and hands that context to
whichever AI you are using — so when one stops mid-task, another picks up
without you re-explaining anything.

The "second brain" framing is retired on purpose. It describes the storage and
misses the product: what makes this worth installing is not that notes are
searchable, it is that work survives the end of a session.

## Principles

1. **Markdown is truth.** Everything the system knows lives in plain `.md` files
   you can read, edit, grep, and take elsewhere. If this project dies you keep a
   vault.
2. **The database is a cache.** `.brain/index.db` holds embeddings, edges and
   FTS. Delete it, reindex, get byte-identical state. It is never authoritative.
   That covers notes, checkpoints *and* memories: memories are written to
   `memories/<kind>.md` and rebuilt from there, so `rm -rf .brain` costs nothing
   but the time to re-embed. It used to be false for memories, which lived only
   in the cache — the continuity benchmark's durability family exists to keep it
   honest.
3. **Local by default.** Nothing leaves the machine unless you name a cloud model
   and confirm the redaction preview.
4. **Propose, don't assert.** The system writes nothing to the vault that you
   haven't accepted, until you raise its auto-accept threshold yourself.

## Two-tier memory

| | Episodic | Semantic |
|---|---|---|
| Store | SQLite (`.brain/index.db`) | Obsidian vault (`.md`) |
| Content | app focus, URLs, files, commits, calendar | people, projects, topics, routines |
| Volume | ~50k rows/day | ~5 notes/day |
| Lifetime | rolled up then pruned (default 90d) | permanent |

The pipeline is `events → rollup → proposed notes → review → vault`. Raw events
never become markdown directly. A vault that grows 400 files a day is landfill.

## Vault layout

```
vault/
  daily/2026-07-18.md
  people/…  projects/…  topics/…  routines/…  sources/…
  sessions/<project>/     # checkpoints: where an agent stopped
  memories/<kind>.md      # what it knows about you, one line each
  .brain/                 # index.db, config.toml, queue.jsonl — do not sync
```

`memories/` is the memory store written down rather than a set of notes, so the
indexer skips it as prose and reconciles it as memories. The bookkeeping —
id, confidence, salience, provenance — rides in an HTML comment, which Obsidian
does not render, so the file reads as a plain bullet list you can edit. Correct
a line to correct the fact; delete a line to forget it.

### Note frontmatter

```yaml
type: person                       # person|project|topic|routine|daily|source
aliases: [Sam, "@sameer"]
relations:
  - { pred: works_on, obj: "[[brain]]", conf: 0.82, src: inferred }
  - { pred: manages,  obj: "[[api-team]]", conf: 1.0,  src: stated }
first_seen: 2026-03-02
observations: 47
```

`conf` (0–1) and `src` (`stated` | `inferred` | `imported`) are what keep an
auto-written vault from rotting. Edges below the confidence floor render dashed
in the graph and are excluded from retrieval context.

Body links (`[[wikilinks]]`) are untyped edges at `conf: 1.0, src: stated` —
you wrote them, so they're true.

## Graph edges come from three places

| Source | Trust | Rendering |
|---|---|---|
| `[[wikilinks]]` in body | absolute | solid |
| typed `relations:` frontmatter | `conf` | solid → dashed by conf |
| embedding similarity > θ | weak | faint, toggleable, never persisted |

Similarity edges are computed at view time and never written to disk. They're a
lens, not a fact.

## Model routing

All four target runtimes (Ollama, LM Studio, Jan, Msty) expose an
OpenAI-compatible `/v1`, so there is one client plus a capability probe. Local
endpoints are auto-discovered by port scan: 11434, 1234, 1337, 10000.

| Tier | Job | Size | Detected here |
|---|---|---|---|
| T0 | embeddings | 137M | `nomic-embed-text` |
| T1 | per-event tag / classify / extract | 3–4B | `gemma3:4b` |
| T2 | daily rollup, entity resolution | 8–24B | `qwen3.6`, `gpt-oss:20b` |
| T3 | weekly synthesis, hard queries | cloud | BYOK, opt-in |

**Use constrained decoding, not tool-calling, for extraction.** Small local
models are unreliable at tool schemas but near-perfect with a JSON schema
enforced at the sampler (Ollama `format`, LM Studio structured output).

## Capture (macOS)

Default on, cheap, high signal:
- frontmost app + window title via `NSWorkspace` / AX API, sampled 5s, coalesced
- browser history read from Chrome/Arc/Safari SQLite
- calendar via EventKit (JXA under osascript, read-only), commits in watched repos
- clipboard text (blocklisted apps excluded)

Default **off**, explicit opt-in:
- periodic screenshot + Vision framework OCR (local, no network)

Always: per-app blocklist (password managers, Messages, banking), auto-pause on
secure text fields, visible menubar recording state, global panic hotkey.

Routine detection is sequence mining over the episodic table, not prompting.
The LLM's job is to *name* a discovered pattern, not to find it.

Calendar is the one source that reaches into the future: upcoming meetings are
captured with their real start times and refreshed wholesale each poll, so a
rescheduled meeting never leaves a ghost. That future window is what lets the
secretary say "standup in 20 minutes".

## Secretary

The system leads; it is not an archive you query. On open it presents a **brief**
assembled from data already captured — imminent meetings first (the only hard
deadline), then open loops stalest-first, then what has gone quiet, then what you
usually do around now. Building a brief runs no model, so it is instant and
offline; the model only extracts open loops upstream during the daily rollup.

The archive surfaces remain, in the background: the ask box sits under every
panel and the timeline is one tab over. Secretary is the default, not the only,
mode.

## App

Wails v2 (idle RAM is a feature for an always-on process). Menubar orb with
state; pull-down panel with today's timeline, ask box, review-queue badge.

Graph view: the ego-graph (focus node + a few hops) is extracted in Go from the
index; the force layout and rendering are done on an HTML canvas in the
frontend. The design originally called for backend layout + WebGL, but ego-mode
keeps the node count small enough (dozens, not thousands) that a canvas
simulation is smooth and far more interactive — you can drag nodes and click to
re-centre. Edges are styled by provenance (solid wikilinks, confidence-graded
typed relations, faint never-persisted similarity edges), there is a time
scrubber over `first_seen`, and click-through opens the note in Obsidian.

## Build order

1. **Vault index + retrieval + ask box.** No capture. ✓
2. App-focus + browser capture → episodic timeline. No LLM. ✓
3. Rollup → proposed notes → review queue. ✓
4. Graph view. ✓
5. Routine mining, proactive nudges. ✓

All five steps implemented, plus a secretary layer, calendar capture, model
router/BYOK, a conversational streaming agent, and an MCP server that lends the
memory to other tools — including session continuity, so one agent can check its
work into the vault and a different agent can resume it.

The vertical personas (tutor, business) and the outbound-action agent harness
were removed: brain is memory that agents query, and neither was that.

## Known failure modes

- **Summary inbreeding** — always re-derive rollups from episodic source rows,
  never from prior summaries.
- **Watcher feedback loop** — atomic temp+rename writes, 2s debounce on our own
  paths.
- **Duplicate notes** — resolve entities *before* creating a note.
- **The creepy moment** — visible recording state + panic hotkey.
