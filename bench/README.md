# The continuity benchmark

A benchmark for what survives when one agent stops and another starts.

Recall benchmarks — LongMemEval and its relatives — measure whether a store can
surface the session holding an answer. That is the easy half. It says nothing
about the case this project exists for: an agent stops mid-task, a *different*
agent arrives, and nobody re-explains anything. Nothing scores that, so nothing
optimises for it.

The suite lives in [`internal/eval`](../internal/eval); this directory holds the
bridges to memory systems that are not written in Go.

## Running it

```sh
go build -o bin/brain ./cmd/brain
./bin/brain bench continuity list          # what is measured, and what we expect to fail
./bin/brain bench continuity               # every system installed on this machine
./bin/brain bench continuity --verbose     # per scenario, with what was missed
./bin/brain bench continuity --dump        # the raw retrieved context, for auditing labels
./bin/brain bench continuity --brain-only  # skip the comparison, ~12s
```

Useful flags: `--only <id|family|skill>` to narrow, `--no-embed` to score
lexical and graph retrieval with no model loaded.

## What it measures

| Metric | Meaning |
|---|---|
| **pass** | met every bar the scenario set, signal labels included |
| **fidelity** | recall × (1 − leakage) — carrying what is needed while keeping out what is wrong |
| **recall** | share of required facts that arrived |
| **leak** | share of facts that should have been suppressed but were not |
| **signal** | share of meta-properties exhibited: that context is stale, that the store does not know, who found a thing |
| **dens/1k** | required facts per 1000 tokens — the price of that recall |

Fidelity is multiplicative on purpose. A response carrying every required fact
alongside a superseded price is not half right: the agent acts on the stale
number, and the correct facts beside it do not undo that.

Scoring is mechanical — substring matching over normalised text against
hand-written gold labels. Every headline number is reproducible with no model
running. The one place the harness asserts rather than measures is the
durability family, and it says so in the report.

Two things the matcher does that are worth knowing:

- **The question is stripped from the answer before matching.** Systems that
  head their output with the task they were given would otherwise satisfy any
  label whose wording overlaps the question. One case scored a clean 100% this
  way before the fix, matching `"after signing"` in an echoed header while
  returning two undated facts in arbitrary order.
- **Surface variants are the scenario author's job.** Write
  `Any: {"71 percent", "71%"}` rather than hoping a cleverer matcher guesses. A
  fuzzy matcher that silently accepts near-misses is how a benchmark starts
  flattering everyone equally.

## The honest half

Roughly a third of the suite is marked `KnownWeakness` — cases brain fails
today. Several were found by an agent picking apart brain's own handoff output
during a live test: stale context presented without its age, circular sourcing
where prose restates a claim the data contradicts, and abstention, which the
LongMemEval harness in `internal/memory/bench.go` filters out before scoring.

The report ends with a **predictions that were wrong** section comparing each
label against what actually happened. A case marked a weakness that starts
passing is progress worth noticing; one marked a strength that starts failing is
a regression the averages would otherwise absorb. Either way the label is now
wrong and somebody has to look.

## The systems compared

Baselines are built in and always run:

- **none** — the floor. Whatever it scores, the suite gives away for free.
- **static-file** — a hand-maintained `CLAUDE.md`. Receives documents only,
  because nobody appends every dead end to a rules file. This is the real
  incumbent: a memory system that cannot beat it is not worth installing.
- **recency-window** — the newest events that fit. What conversation compaction
  approximates: no notion of relevance, only of lateness.
- **full-dump** — the entire history, newest first, to the budget. The ceiling,
  and the reason density is a headline column.
- **vector-rag** — top-k cosine, no lexical arm, no graph, no checkpoint. What
  most "memory layers" reduce to once the marketing is removed.

Third-party systems run as subprocesses over a JSON-lines bridge
(`internal/eval/adapters/bridge.go`) and are skipped with a printed reason if
not installed — a comparison table with a quietly missing column flatters
whoever is left.

```sh
cd bench/adapters
uv venv .venv-mem0       && VIRTUAL_ENV=.venv-mem0 uv pip install "mem0ai[extras]" ollama
uv venv .venv-mempalace  && VIRTUAL_ENV=.venv-mempalace uv pip install mempalace
```

Both are pointed at Ollama, so no API keys are needed and the comparison is
like-for-like: same models, same hardware, no network.

### Fairness notes

Each adapter is written to make its system look as good as that system can look.
brain uses checkpoints because it has them; a store with only `add()` and
`search()` receives the same information flattened into complete prose rather
than withheld. Where a system loses, it should lose for what it is, not for how
it was driven here.

- **mem0** is run with `infer=False`. Its `add(infer=True)` path runs an LLM
  over every write to extract and reconcile facts — its real design, and its
  real advantage on contradictions — but it is one model call per event, and a
  single scenario writes over two hundred. `infer=False` stores text verbatim
  and embeds it, which for a retrieval benchmark is if anything favourable:
  nothing is lost to extraction. `MEM0_INFER=1` runs it the authentic way.
  `fastembed` is installed so its BM25 arm is available, since brain is scored
  with a lexical arm too.
- **mem0 telemetry is disabled** in the shim. It ships analytics on by default
  and opens a PostHog client at import; a benchmark claiming every system runs
  locally has to mean it.
- **MemPalace** is the only third-party system here with a handoff story of its
  own — it ships an `artifact` command for agent handoffs and a `wake-up`
  context command. The benchmark does not use them: `artifact` is an exact-file
  exchange rather than a retrieval surface, and wiring it would score a
  different feature than the one under test.

## Adding a system

Implement `eval.Adapter` in Go, or drop a `<name>_adapter.py` in
`bench/adapters/` speaking the bridge protocol: one JSON object per line in, one
per line out, ops `reset` / `write` / `read` / `close`, plus `--probe` exiting
non-zero with a readable reason when the system cannot run. `Discover()` picks
it up automatically.

Systems that distinguish a source of truth from a rebuildable cache should also
implement `eval.Durable`. Not implementing it is itself an answer, and the suite
reports it as one.
