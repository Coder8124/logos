# feat/dream — the nightly consolidation pass — step 8

Built after the secretary, on purpose. Dreaming reorganises what the system
already knows; there is no point running it over a memory store that is still
empty and a routine baseline that is still forming. It is the first pass whose
whole input is the product of the earlier steps.

## The core point

Sleep is not summarisation. A nightly pass that "compresses the day" is just a
late rollup. Biology does **four distinct things** while offline, in two phases,
and the value is in keeping them distinct:

- **NREM** (early, cheap, arithmetic + T1) — *stabilise*: replay salient
  experience, extract gist, downscale the field, wire artifacts to their work.
- **REM** (late, expensive, T2) — *recombine*: surface non-obvious connections
  across distant work and propose new threads.

Order is load-bearing. REM recombines the **already-abstracted** representations
NREM produced, so consolidation runs to completion before recombination starts.
Creativity over a cleaned, compressed field is sharper and cheaper than
creativity over 50k noisy events.

Everything here obeys the two house rules already in force:

- **Compute, then narrate.** *What* to replay, downscale, and bridge is chosen by
  Go arithmetic — salience, cosine, graph adjacency, routine counts. The model
  only phrases a gist or proposes a connection. It never picks the numbers.
- **Propose, don't assert.** Nothing the dream produces touches the vault or the
  memory store unattended. NREM's structural edits are the same deterministic
  maintenance already trusted to run headless; every *inference* — a gist, a
  connection — is a proposal in the review queue, capped below any auto-accept
  threshold.

## Package and surface

New package `internal/dream` (`dream.go` orchestration, `nrem.go`, `rem.go`,
`dream_test.go`). New command `cmd/brain/dream.go`. Wired to run from the daemon
on a local-midnight timer for the day that just ended.

```text
brain dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
```

Idempotent per date, like `rollup.Day`. Skips cleanly when the day has no
sessions. First N nights run `--dry-run` implicitly (report only) until the user
raises the auto-accept threshold — the same trust ramp the note queue uses.

```go
type Result struct {
    Date            string
    Replayed        int // salient memories/sessions re-affirmed
    Merged          int // near-duplicates folded (from Consolidate)
    Superseded      int // stale facts replaced (from Consolidate)
    GistsProposed   int // episodic→semantic abstractions queued
    Downscaled      int // memories touched by the homeostatic pass
    Linked          int // artifact↔project (and, later, ↔episode) edges
    InsightsProposed int // REM connections queued for review
}
```

---

## Phase A — NREM (stabilise)

Runs first, always. Cheap enough to run every night on a laptop: three of its
four jobs touch no model at all.

### 1. Prioritised replay

The hippocampus replays *salient* experience, not the whole day. So does this.

- Load the day's events (`capture.Range`), the day's sessions (`rollup.Sessionise`),
  and the memories created or recalled today. Rank by `memory.EffectiveSalience`.
- Re-affirm the top slice: this is exactly `memory.Consolidate(db, rt)` — fold
  near-duplicates, supersede stale facts — run against the day's cohort rather
  than the whole store, so cost is proportional to what actually moved today.
- Reinforcement of what got used is already handled at recall time (`Recall`
  bumps `uses`/`last_used`); replay adds nothing there and must not double-count.

Model: T1, via the existing `rt.ModelFor(router.T1, true)` inside `Consolidate`.
Everything else in this job is arithmetic.

### 2. Gist extraction — the one genuinely new capability

Sleep extracts the *rule* from the specifics. This is where episodic detail
becomes semantic knowledge — the step rollup never took, because rollup
summarises ("you did X, Y, Z") rather than generalises ("you *always* do X").

Two sources, two trust levels:

- **Arithmetic gist (stores directly, low confidence).** `routine.FindPeriodic`,
  `FindSequences`, and `FindAnomalies` already mine the episodic tier and clear
  hard support thresholds (`MinOccurrences`, `MinWeeks`, `MinConsistency`). A
  pattern that clears them is a *count*, not a guess, so it mints a semantic
  memory directly: e.g. a stable Sunday-evening loop-clearing pattern becomes a
  `memory.Context` memory "clears commitments Sunday evenings", stored via
  `memory.Store` with `Source: "dream"`.
- **Abstracted gist (proposes only).** Where the abstraction requires the model —
  collapsing several episodic memories into one preference or standing-context
  fact — T1 writes the candidate under a JSON schema, and it goes to the **review
  queue**, never straight to the store.

Add a `"dream"` case to `defaultConfidence` returning **0.5**: an inferred fact
is a hypothesis that corroboration through the normal `Store` reinforcement path
must earn upward. A gist that never recurs decays out on its own.

### 3. Homeostatic downscaling (SHY)

The synaptic-homeostasis hypothesis: sleep globally *renormalises* weights so
only the strongest survive, preserving signal-to-noise. This is the real answer
to "stays clean without bloating" — cleanliness by construction, not by pruning.

`memory.Decay` today drifts each memory toward its own effective salience. Add a
small **global** multiplicative downscale (e.g. `salience *= 0.98`, keeping the
existing `0.05` floor) across all active memories, so the whole field renormalises
nightly and reinforced memories stand out by *relative* height, not just absolute.

**Do not log the downscale per memory.** It touches every row; a `memory_log`
line each would bury the timeline under 10k nightly entries that carry no
narrative. Only structural events (merge, supersede, new gist) log — the
timeline stays legible, which was its whole point.

### 4. Artifact association — the on-thesis half of "artifact organization"

Tie the day's `file` / `commit` / `url` events to the work they belong to. This
is provenance, not a dev copilot: brain does **not** parse or index code by
language/task (that is Cursor's job and off-thesis). It records *that this
artifact mattered to this project and was in play today*.

- Artifact→project is largely done: `project` assembles files/progress from the
  event log and `projects sync` tags memories. The dream pass emits the
  corresponding `graph` edges so the relationship view shows them.
- Artifact→**episode** (the conversation an artifact was discussed in) is the
  higher-value link and is **blocked on persisted conversations** (roadmap). Once
  episodes are first-class, this becomes a small association job here — associate,
  never create.

No model. Pure event-log + graph arithmetic.

---

## Phase B — REM (recombine)

Runs only after NREM completes, over the compressed field. This is the mirror's
engine: non-obvious connections, cross-project analogies, candidate new threads.

### 5. Recombination

- Sample candidate pairs/clusters by arithmetic: memories or notes that are
  *distant* (low direct similarity) yet share a graph neighbour, or that bridge
  two different projects. Selection is Go; the model never trawls the store.
- Hand each candidate to **T2** (`rt.ModelFor(router.T2, true)`) to propose the
  connection or new thread, under a schema.
- If only T1 resolves (router degraded), REM runs shallow or skips with a warning
  — the same graceful-degradation contract the router already honours. A missed
  night of dreaming is worth more than a fabricated one.

### The three guardrails — propose-don't-assert applied to the subconscious

REM is exactly where a memory system can poison itself: creative recombination
on a small local model *will* invent links that were never real (humans form
false memories in sleep for the same reason). The trust loop already has the
antidote; apply it without exception.

1. **Proposals only, never writes.** REM output is enqueued for review and
   surfaces in `brain brief` ("overnight I noticed —"). Nothing is stored until
   accepted. Reuse the review-queue shape (`Enqueue`/`Approve`, as in `action`
   and `rollup`); add an `insight` proposal kind whose acceptance writes a memory
   or a note.
2. **Cite both endpoints.** Every insight carries the two memory IDs / note slugs
   it bridges, as `Evidence`. **A connection that cannot name both endpoints is
   dropped before it is ever enqueued** — that is the hallucination filter.
3. **Low seed confidence, corroboration to promote.** An accepted insight is
   stored at the `"dream"` confidence (0.5) and rises only through the normal
   reinforcement path when reality corroborates it later. It never outranks a
   stated fact at equal relevance — recall already scales by confidence.

Cap REM at `max_insights` per night (config, default **3**). A queue flooded with
speculative connections is a queue the user stops reading, and an unread queue is
the same as no confirmation gate at all.

---

## Tier map

| Job | Phase | Model | Why |
|---|---|---|---|
| Replay / consolidate | NREM | T1 | duplicate/supersede classification, already T1 |
| Arithmetic gist | NREM | — | routine mining is counts, not inference |
| Abstracted gist | NREM | T1 | collapse specifics → one fact, under schema |
| Downscaling | NREM | — | one multiply per row |
| Artifact links | NREM | — | event-log + graph adjacency |
| Recombination | REM | T2 | the one place per night worth real reasoning |

NREM is nearly free; the entire per-night model budget is REM's handful of T2
calls. That is deliberate: stability should never be rationed, only creativity.

## Reliability notes

- **Single-connection discipline.** The pass touches many tables. Load each fully
  and close the cursor before mutating (`activeMemories` is the pattern); call
  `logEvent` only outside open row loops — the one-connection pool deadlocks
  otherwise. This is the same rule the rest of `internal/` follows.
- **Determinism.** Given the same day's events, NREM is fully reproducible; only
  REM's phrasing varies. `--dry-run` reports every intended change without
  writing, so the pass is auditable before it is trusted.
- **Config** (`.brain/config.toml`):
  ```toml
  [dream]
  enabled      = true
  hour         = 0      # local hour to run (midnight)
  rem          = true   # disable to run stabilise-only on constrained machines
  max_insights = 3
  ```

## Test plan

- `dream_test.go` on the scratch vault (`scratchpad/testvault`), never the real
  one — same rule as capture backfill.
- NREM: a fixture day with a repeated pattern must mint exactly one gist memory at
  confidence 0.5; a second identical night must **not** add a twin (dedup holds);
  downscaling must lower salience without crossing the floor and without writing
  a `memory_log` line.
- REM: an insight whose two endpoints resolve is enqueued; one with a missing
  endpoint is dropped; accepting an insight stores it at 0.5, and a later
  corroboration raises it via the normal path.
- Degradation: with T2 absent, REM skips with a warning and NREM still completes.
- Run once against the real 122k-event vault in `--dry-run` before shipping —
  fixtures miss what real data catches.
