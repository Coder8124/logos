# A benchmark for handoff, not just recall

**Nine memory systems, thirty-two hand-authored scenarios, one machine, no
network.** Run 2026-08-29 on an M-series Mac, every system embedding with
`nomic-embed-text` through the same local Ollama.

Reproduce it:

```sh
go run ./cmd/brain bench continuity          # the whole field
go run ./cmd/brain bench continuity --brain-only
go run ./cmd/brain bench continuity list     # every scenario and what it asks
```

---

## 1. Why another memory benchmark

Existing memory benchmarks — LongMemEval, LoCoMo, and the retrieval suites that
follow them — measure the same thing in different clothes: *given a long
history, can the system find the fact that answers a question.* brain scores
well on that (96.0% recall@5 on the full 500-question LongMemEval-S, hybrid
retrieval beating vector-only by 8.9 points) and so does everything else worth
comparing against. Recall is close to solved at this scale.

The thing that is not solved, and that nobody measures, is **handoff**: an agent
stops mid-task, and a different agent — often a different product, on a
different day — has to continue. That is a different problem from recall in four
specific ways, and each one is a family of scenarios here:

1. **Negative knowledge.** The expensive thing an agent learns is what *didn't*
   work. Recall benchmarks never test it, because it isn't an answer to a
   question — it's a constraint on a plan.
2. **Supersession.** A history contains a price that changed, a plan that was
   cancelled, a decision that was reversed. Returning the stale one is worse
   than returning nothing, and recall metrics score it as a hit.
3. **Provenance and staleness.** *Who* recorded this, and *when*, decide whether
   the next agent should act on it. A retrieved sentence with neither is a
   liability.
4. **Abstention.** When nothing on record bears on the question, the correct
   output is to say so. Nearest-neighbour retrieval structurally cannot do this:
   there is always a nearest neighbour.

A fifth family is about the substrate rather than the retrieval:
**durability** — does what the system knows survive deleting its own cache.

### The metric that matters

Recall alone rewards dumping the whole history. So a scenario passes only when
it clears **every** bar it sets:

| Metric | What it measures |
|---|---|
| **carry** | required facts present in the output |
| **leak** | facts that must *not* appear — superseded values, other projects' dead ends, cancelled plans |
| **signal** | required framing — "this was tried", "this changed", "nothing on record" |
| **tokens** | output size, against the scenario's budget |

**fidelity** = carry × (1 − leak). **pass** requires all three of carry, leak
and signal. That last condition is the whole design: a system that returns the
right fact *and* the stale one it replaced has not answered the question, it has
handed the next agent a coin flip.

---

## 2. The field

Six real systems and three controls. The controls exist so a number has
something to mean.

| System | What it is |
|---|---|
| **brain** | this project — checkpoints in markdown, hybrid retrieval, budgeted context assembly |
| **letta** | Letta 0.16.8 (formerly MemGPT), archival memory via a local server |
| **mem0** | mem0ai, verbatim store + BM25/vector search |
| **mempalace** | MemPalace, local palace store |
| *vector-rag* | control: embed everything, return top-k by cosine |
| *recency-window* | control: return the last N events |
| *full-dump* | control: return everything, truncated to budget |
| *static-file* | control: a hand-written project file, never updated |
| *none* | control: no memory at all. The floor. |

Every system runs locally against the same models. No API keys, nothing leaves
the machine, so the comparison is like-for-like on the same hardware.

---

## 3. Results

```
system                pass  fidelity  carry   leak  signal  tokens  dens/1k
brain                81.2%    82.8%   89.1%  33.3%   88.9%     253      6.5
mempalace            46.9%    71.9%   82.8%  58.3%   22.2%     308      4.0
recency-window       46.9%    68.8%   84.4%  83.3%   22.2%     230     11.0
full-dump            46.9%    68.8%   84.4%  83.3%   22.2%     264     10.9
letta                43.8%    67.2%   82.8%  83.3%   22.2%     169     11.2
mem0                 43.8%    67.2%   82.8%  83.3%   22.2%     169     11.2
vector-rag           43.8%    67.2%   82.8%  83.3%   22.2%      96     12.9
static-file           6.2%    22.7%   22.7%   0.0%    0.0%      14      6.4
none                  0.0%     6.2%    6.2%   0.0%    0.0%       0      0.0
```

By family:

```
system              continuity  durability  memory
brain                      86%        100%     73%
mempalace                  50%          0%     53%
recency-window             50%          0%     53%
full-dump                  50%          0%     53%
letta                      50%          0%     47%
mem0                       50%          0%     47%
vector-rag                 50%          0%     47%
static-file                 0%         33%      7%
none                        0%          0%      0%
```

### What the numbers say

**Carry is not the differentiator.** Every real system lands between 82.8% and
89.1%. Retrieval works. The spread in *pass* comes almost entirely from leakage
and signal.

**Leakage is.** brain leaks 33.3%; every embedding-based system except MemPalace
leaks 83.3% — they return the superseded price alongside the current one,
because both are semantically close to the question and nothing in a cosine
score encodes "this one was replaced."

**Signal is the cliff.** brain 88.9%, everything else 22.2%. No system in the
field except brain says "this was already tried", "this value changed", or
"nothing on record covers that." This is not a tuning gap; it is a category
these systems do not model.

**Density is a trap.** vector-rag carries the most facts per 1000 tokens (12.9)
and passes 43.8%. It buys that density by returning nothing but nearest
neighbours — no provenance, no ordering, no framing. Efficiency at the cost of
everything that makes the context actionable.

### Letta and mem0 scoring identically is not a coincidence

Both land on 43.8% / 67.2% / 82.8% / 83.3% / 22.2% at 169 mean tokens. With
extraction disabled, they reduce to the same algorithm: store the event text
verbatim, embed it with `nomic-embed-text`, return top-k by cosine within
budget. Same corpus, same embedding model, same ranking — so the same passages
come back. The identical row is evidence the harness is doing what it claims,
and a reminder of what is actually being compared for those two: their retrieval
substrate, not their agent loops.

---

## 4. Where the difference comes from

Eight skills scored 0% across the entire field in the first run of this suite.
brain now holds seven of them, and no other system moved:

| Skill | brain | best of the rest |
|---|---:|---:|
| staleness | **100%** | 0% |
| supersession | **100%** | 0% |
| abstention | **100%** | 0% |
| attribution | **100%** | 0% |
| scope | **100%** | 0% |
| died-mid-task | **100%** | 0% |
| source-of-truth | **100%** | 33% (static-file) |
| conflict | **50%** | 0% |

These came from behaviour changes, not from fitting the suite — each one shows
up on scenarios other than the one that exposed it:

- **Age and author on everything uncommitted.** Working notes and checkpoints
  carry who wrote them and how old they are, with an explicit warning past seven
  days. → staleness, attribution.
- **Two-tier checkpoint budget.** Decisions, dead ends, open questions and the
  next step are charged before the session log. Forty standup lines used to
  evict "already tried, didn't work". → distractors.
- **Checkpoint history carried forward.** Predecessors' ruled-out approaches
  accumulate across handoffs, attributed. → multi-hop-handoff.
- **Supersession at recall.** Where two retrieved statements are on-topic and
  assert different values, the later wins and the earlier is dropped — reported
  as "this changed", *without* reprinting the dead value. → supersession.
- **Cancelled plans suppressed.** A next step withdrawn by a later decision is
  replaced by the decision that withdrew it. An agent handed "next step: X" acts
  on X. → superseded-plan.
- **Contradiction flagged, not resolved.** Two sources that disagree with no
  ordering between them both stay, and the disagreement is named. → conflict.
- **Abstention.** When nothing retrieved is on topic, say so rather than letting
  the nearest neighbour stand in as an answer. → abstention.

### Durability: 100% vs 0%

Three scenarios write, delete every rebuildable artifact, and read again. brain
scores 100%; every other system scores 0%, including the controls.

This is not a subtle result and it is not really about retrieval quality — it is
about where the source of truth lives. brain writes checkpoints and memories to
markdown in a vault the user owns; the SQLite index is a cache, and deleting it
costs only the time to re-embed. The other systems keep their knowledge inside
their own store, so wiping the store wipes what they know.

This family exists because it was **once false for brain too**. Memories lived
only in the cache, and `rm -rf .brain` — which the README told people to do —
destroyed every preference and fact with no warning. The benchmark caught it,
and the family now exists to keep the claim honest.

---

## 5. Where brain loses, and where nobody wins

Reported because a benchmark that only shows wins is marketing.

- **arithmetic — 0%, everyone.** Aggregating values across records ("what do
  these six line items total"). No system in the field does arithmetic over
  retrieved facts. Retrieval is not computation.
- **recency-conflict — 0%, everyone.** Two sources disagree *and* one is newer,
  requiring the system to prefer recency without being told to.
- **temporal — brain 0%, MemPalace 50%.** Ordering events in time and answering
  windowed questions. **MemPalace beats brain outright here** and it is the one
  skill where that is true.
- **multi-hop — brain 0%, recency-window and full-dump 100%.** Chaining two
  facts to reach a third. The dumb controls win by carrying everything, which is
  exactly the tradeoff their leak scores pay for — but a loss is a loss.
- **conflict — 50%.** Half the contradiction cases are still unflagged.

---

## 6. Caveats

Stated plainly, because they bound what these numbers are worth.

- **32 scenarios.** One case moves the headline by roughly 3 points. Treat
  differences under ~6 points as noise.
- **I wrote the suite and I wrote the system it scores best.** Six cases are
  marked as known weaknesses up front and the report prints every wrong
  prediction, but this is not independent evaluation and should not be read as
  one.
- **The headline table runs mem0 with `infer=False` and Letta with its agent
  loop off.** Both then store text verbatim rather than running an LLM over
  every write — favourable to them on retrieval, since nothing is lost to a
  small model's extraction, and unfavourable on reconciliation, which is
  precisely where their headline weakness (supersession) sits. This was the
  caveat most likely to matter, so it was measured rather than left standing.
  `MEM0_INFER=1 LETTA_AGENT_LOOP=1` runs both authentically; see
  [below](#61-what-changes-when-they-run-authentically).
- **Budgets are generous relative to scenario size**, which is why dumping the
  whole history still scores 46.9%. A tighter budget would separate the field
  further and would also be a different benchmark.
- **Single machine, single embedding model.** No variance across hardware,
  seeds, or embedding choice is reported.
- **Letta needs PostgreSQL + pgvector and a running server**; it is the only
  system here that cannot be run from a pip install alone.

### 6.1 What changes when they run authentically

Measured on `supersession-current-value` — three statements of a retail price
($199, then $229, then a final $249) buried in twenty noise facts. The scenario
requires carrying $249 and suppressing both earlier numbers, so it is the
single case the caveat most directly predicted. Both modes were run on the same
machine, back to back. `brain` and MemPalace are unaffected by these flags and
score identically in both runs, which is what makes the two comparable.

| system | default | authentic | |
|---|---|---|---|
| brain | 100% | 100% | control |
| **letta** | 0% (leaks both) | **50% (leaks one)** | improved |
| **mem0** | 0% (leaks both) | **0%** | no gain |
| mempalace | 50% | 50% | control |

Fidelity, on one scenario. Two things worth stating separately:

**Letta's loop does the work the shortcut skips.** In archival mode all three
prices come back, the same failure as plain vector search. With the loop on,
`$199` is absent from the output entirely — the agent never filed it, or filed
it and removed it. That is genuine reconciliation, and the caveat was right that
turning it off cost Letta credit here. It still leaks `$229`, so the scenario is
still a fail: `pass` is conjunctive and one surviving leak reads the same as
leaking everything. The improvement is real and is only visible in `fidelity`.

**mem0's inference made it worse, not better.** With `infer=True` it reconciled
the three statements into one synthesised fact:

> Retail price is currently $199, but will increase to $229 after the optics
> quote and then to $249 for launch

It read a history of revisions as a schedule of future increases, and asserts
the current price is the oldest superseded value. That is worse than storing
verbatim: `infer=False` at least leaks the old prices as separately dated facts
an agent could disambiguate, where inference produces one confident sentence
stating the wrong number. The same pass sprayed duplicates of unchanged noise
facts into the store — "Design review notes are filed under the sprint folder"
five times.

**The cost, measured.** The suite is 596 write events across 32 scenarios, 201
of them in `scale-haystack` alone. Letta's loop took **12m29s for 23 events —
32.6 s/event, and 108 chat completions for 23 writes, about 4.7 model calls
each**, because a turn is reason → call a tool → read the result → often step
again. mem0's `infer=True` ran nearer 10 s/event. Extrapolated, the full
authentic suite is **~7 hours**, and that is a floor rather than an estimate:
both systems grow their context as memory accumulates — Letta's context estimate
climbed 2,379 → 8,917 tokens over these 23 events — so a 201-event scenario is
superlinear, not 8.7× a 23-event one.

One fairness gap remains open: mem0's `infer=True` path asks for spaCy
(`mem0ai[nlp]`), which is not installed here, so it falls back with a warning.
The headline `infer=False` numbers do not touch that path.

---

## 7. Reproducing

```sh
# brain alone — needs only Ollama
go run ./cmd/brain bench continuity --brain-only

# the whole field — needs the Python adapters installed
python3 -m venv bench/adapters/.venv-mem0    && bench/adapters/.venv-mem0/bin/pip install 'mem0ai[extras]'
python3 -m venv bench/adapters/.venv-letta   && bench/adapters/.venv-letta/bin/pip install letta asyncpg pgvector
letta server --port 8289          # needs PostgreSQL with pgvector
go run ./cmd/brain bench continuity
```

An adapter that cannot import its own package is **skipped, not failed** — a
missing row is honest where a row of zeros would be a lie about that system's
quality. See `bench/README.md` for the adapter protocol; each is ~100 lines of
Python translating the harness's events into that system's own API.

Scenario definitions live in `internal/eval/scenarios.go`, scoring in
`internal/eval/score.go`. Every scenario carries a `Why` line stating what it is
really asking, and a `Known` label recording brain's expected outcome so
regressions are visible rather than quietly absorbed.
