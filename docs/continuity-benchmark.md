# The continuity benchmark — results

> **This page reports the 2026-08-25 baseline**, before any of the failures
> below were addressed. Fixes have since landed for staleness, supersession,
> abstention, attribution, project scoping, distractor resistance and the
> checkpoint-truncation bug, and the suite's own labels have moved with them.
> The numbers here are kept as measured rather than quietly restated, because a
> benchmark that edits its history the moment its author improves is not
> evidence of anything. Re-run `brain bench continuity` for current figures.

32 hand-written scenarios, 8 systems, everything local. Run with
`brain bench continuity`; harness in [`internal/eval`](../internal/eval),
methodology and fairness notes in [`bench/README.md`](../bench/README.md).

Measured 2026-08-25 on Apple Silicon, `nomic-embed-text` for every system that
embeds. brain: `feat/continuity-benchmark`. mem0 2.0.19, MemPalace 1.x, both
pointed at Ollama, both installed from PyPI that day.

## Headline

| system | pass | fidelity | recall | leak | signal | tokens | dens/1k |
|---|---:|---:|---:|---:|---:|---:|---:|
| **brain** | **50.0%** | 67.2% | 82.8% | 83.3% | **33.3%** | 276 | 5.4 |
| mempalace | 46.9% | **71.9%** | 82.8% | **58.3%** | 22.2% | 308 | 4.0 |
| recency-window | 46.9% | 68.8% | **84.4%** | 83.3% | 22.2% | 230 | 11.0 |
| full-dump | 46.9% | 68.8% | **84.4%** | 83.3% | 22.2% | 264 | 10.9 |
| vector-rag | 43.8% | 67.2% | 82.8% | 83.3% | 22.2% | **96** | **12.9** |
| mem0 | 43.8% | 67.2% | 82.8% | 83.3% | 22.2% | 169 | 11.2 |
| static-file | 6.2% | 22.7% | 22.7% | 0.0% | 0.0% | 14 | 6.4 |
| none | 0.0% | 6.2% | 6.2% | 0.0% | 0.0% | 0 | 0.0 |

`pass` = met every bar the scenario set, signal labels included.
`fidelity` = recall × (1 − leakage). `dens/1k` = required facts per 1000 tokens.

**brain leads on pass rate by one scenario.** That is the whole margin. Anyone
reporting this as a decisive win is misreading it.

## What the table actually says

**We win the two things the suite was built to measure, and only those.**

| skill | brain | mempalace | mem0 | vector-rag | full-dump | static-file |
|---|---:|---:|---:|---:|---:|---:|
| died-mid-task | **100%** | 0% | 0% | 0% | 0% | 0% |
| source-of-truth | **67%** | 0% | 0% | 0% | 0% | 33% |
| distractors | **0%** | 100% | 100% | 100% | 100% | 0% |

*died-mid-task* is the agent killed before it could check in. Every system
retrieved the working notes — mem0 and MemPalace both scored 100% fidelity on
the facts. Only brain marked them **uncommitted**, which is the difference
between "here is what is known" and "here is work that was never written down
properly." Everyone else hands the next agent unfinished findings with no sign
they are unfinished.

*source-of-truth* deletes every rebuildable artifact and asks again. brain
survives at 67% because checkpoints and notes are markdown files. Every
vector-backed system scores 0 — not because retrieval failed but because there
was nothing left to retrieve. static-file gets 33%: a file survives, which is
its entire appeal.

*distractors* is ours to lose and we lose it, alone, at 0% against a field of
100%. See the bug list below.

**A hand-maintained `CLAUDE.md` scores 6.2%.** That is the incumbent, and it is
the one unambiguous result here: it carries architecture and nothing that
happened. Any memory system that cannot beat it is not worth installing — and
every real system does, by 37+ points.

**Dumping the whole history is competitive.** full-dump and recency-window both
hit 46.9%, within one scenario of brain, with no retrieval at all. The suite
budgets are generous enough (700–4000 tokens) that brute force works. Their cost
shows in density, not pass rate — and against a real 200-session history rather
than a 40-event scenario, they would not fit at all. Read that row as a warning
about the benchmark's scale, not as a win for dumping.

**vector-rag is the density winner at 12.9** — more than double brain's 5.4 —
and it does it while matching brain's fidelity exactly. brain spends 276 tokens
where naive top-k spends 96. Some of that is real (checkpoint structure, source
citations, budget accounting) and some is bloat we have not measured separately.

## Where MemPalace beats us

It is the only system with a stronger fidelity number: **71.9% vs our 67.2%**,
driven by leakage — **58.3% vs our 83.3%**. It carries fewer things it should
have suppressed. It also took *temporal* to 50% where the entire rest of the
field, brain included, scored 0%.

It is a serious system and it wins these fairly. It is also, notably, the only
third-party system here with its own handoff story — an `artifact` command for
agent exchange and a `wake-up` context command. Neither is measured here (see
the fairness notes), so this table is not the last word on it.

## What nobody can do

Eight of the twenty-six skills scored **0% across every system tested**:

| skill | what it asks |
|---|---|
| abstention | say "nothing recorded" instead of returning a plausible neighbour |
| arithmetic | sum five numbers spread across five notes |
| attribution | say which of two agents found which thing |
| conflict | flag that two sources disagree instead of picking one silently |
| recency-conflict | keep an old hard blocker above five newer trivia |
| scope | resume one project without importing another's ruled-out approaches |
| staleness | say that the note you are relying on is thirteen days old |
| supersession | return the current price without also handing over the two dead ones |

These are not brain weaknesses. They are **category weaknesses** — the entire
field, commercial and baseline alike, fails all eight identically. Every system
tested is a retrieval engine with no model of time, provenance, contradiction,
or ignorance. That is the most useful thing this benchmark found, and it is the
part worth building against.

The starkest is abstention. LongMemEval has abstention questions and
`internal/memory/bench.go` **filters them out before scoring**, as the benchmark
instructs. So the 96.0% recall@5 in [memory-benchmark.md](memory-benchmark.md)
is measured on a set where "I don't know" is never the right answer. Both
numbers are true; they are not measuring the same thing.

## Bugs this found in brain

Recorded, not fixed. Fixing them mid-benchmark would mean reporting numbers from
a version tuned to the suite.

**1. Noise in the working tree destroys the handoff.** `session.Commit` folds
every uncommitted project note into `State`, and `vaultnote.Markdown` renders
State *before* `Didn't work`. When the budget trims on a line boundary, it cuts
from the bottom — so forty routine standup notes push out the failed approaches,
the open questions and the next step. This is why *distractors* is 0%: brain
returned 832 tokens of "Standup: no blockers" and dropped both gold facts. The
single most valuable field is ordered after the least valuable one.

**2. Project scoping leaks.** *handoff-scope-isolation* carries both required
facts and then also hands over the other project's CMS decision. Two projects in
one store, and resuming one imports the other's ruled-out approaches.

**3. Memories do not survive `rm -rf .brain`.** DESIGN.md principle 2 says the
database is a cache you can delete and rebuild. Memories live only in
`.brain/index.db`. Verified directly:

```console
$ brain memory add "I prefer terse replies with no preamble"
$ brain memory                    # 1 memories
$ rm -rf $BRAIN_VAULT/.brain && brain index
$ brain memory                    # no memories yet
$ brain search "launch price"     # the vault note is still there
```

Notes are truth; memories are not. The principle holds for half the system.

**4. Superseded values are handed over alongside the current one.**
*supersession-current-value* returns $249 **and** the two dead prices, with
nothing marking which is live. brain has supersession machinery
(`superseded_by`, `EvSuperseded`); recall does not use it to suppress.

## Predictions that were wrong

The suite records what brain was expected to do so that drift is visible. Seven
of 32 labels were wrong on first run — five optimistic, two pessimistic:

*Expected to pass, did not:* `handoff-attribution` (carried both findings,
never said who found which), `handoff-scope-isolation`, `handoff-noise-resistance`,
`recall-multi-hop` (found the link, lost the person), `supersession-current-value`.

*Expected to fail, passed:* both negation cases. brain stores facts verbatim, so
"we decided against Rust" survives intact — a real property, and one that a
system doing LLM summarisation at write time would likely lose. The labels are
now corrected in the suite.

## Two harness bugs, for the record

The benchmark had to be debugged against itself before its numbers meant
anything. Both would have inflated brain specifically:

- **The question could answer itself.** brain's render opens with
  `# Context for: <task>`, so any gold label whose wording overlapped the
  question matched the echo. `temporal-ordering` scored a clean 100% while
  returning two undated facts in arbitrary order. The matcher now strips the
  verbatim query before scoring.
- **Fidelity ignored signal.** The staleness case scored 100% while doing
  exactly what it was written to catch. `Pass()` now requires every bar the
  scenario sets, not the most flattering one.

Both are covered by tests in `internal/eval/eval_test.go` so they cannot come
back quietly.

## Caveats

- **32 scenarios is small.** Single-case differences move the headline by 3
  points. Treat the per-skill columns as the signal and the overall pass rate as
  a summary, not a ranking.
- **The author wrote the suite.** The counterweight is that 13 of 32 cases are
  marked known weaknesses and the report prints every wrong prediction, but this
  is not an independent evaluation and should not be read as one.
- **mem0 ran with `infer=False`**, storing text verbatim rather than running its
  LLM extraction on every write. That is favourable to mem0 on recall and
  unfavourable on the contradiction cases, which is where its reconciliation
  would have helped. `MEM0_INFER=1` runs it authentically; a full pass at ~200
  model calls per scenario was not affordable here.
- **Budgets are generous relative to scenario size**, which is why brute-force
  dumping stays competitive. A longer-horizon variant is the obvious next
  version.
