# A Continuity Benchmark for Agent Memory

**What survives when the agent doesn't.**

Run of 2026-08-29 · commit `19dad1c` · 32 scenarios · 9 systems · all local

---

## Abstract

Memory benchmarks for language agents almost all measure *recall*: given a
corpus of past conversation, can the system find the fact that answers a
question. That is a real capability and it is not the one that fails in
practice. What fails is **handoff** — an agent stops, another starts, and the
expensive knowledge (what was already ruled out, what was superseded, who said
it, how old it is) does not make the trip.

This benchmark measures handoff. It comprises 32 hand-authored adversarial
scenarios across three families — continuity, durability and memory — scored by
a deterministic rubric with no model in the loop. Nine systems are evaluated:
`brain`, three third-party memory systems run for real against local models
(mem0, MemPalace, Letta), four controlled baselines, and an empty control.

Two results stand out. First, **eight of twenty-five skills were scored at 0%
by every system in the field** at baseline — they are category-level blind
spots, not implementation gaps. Second, at the retrieval layer with inference
disabled, **mem0 and Letta produce byte-identical output**, which suggests the
differentiation between such products lives in their LLM-mediated write path
rather than in retrieval.

`brain` scores 81.2% against a field clustered at 43.8–46.9%. That number is
reported alongside a conflict of interest that should be read first: the author
of the benchmark is the author of the leading system, and six of the eight
originally-universal failures were fixed in `brain` after they were discovered
by this suite.

---

## 1. Motivation

An agent session ends for mundane reasons: a context window fills, a process is
killed, a user switches from one tool to another. The next agent begins from
nothing. In practice the user re-explains the project, and — the expensive part
— the new agent proposes something that was tried and rejected three weeks ago.

Existing benchmarks do not capture this. LongMemEval, LoCoMo and similar suites
pose a question against a conversational history and score whether the right
passage is retrieved. Under that framing a system that returns *every*
statement it has ever seen scores well. In a handoff, that same system hands
the next agent a superseded price, a cancelled plan, and a dead end presented
as an open option.

So the question here is not "can it find the fact" but:

> Given everything one agent knew, can a different agent continue the work
> without repeating it?

That reframing changes what must be measured. Recall is necessary but no longer
sufficient; what a system *wrongly includes* matters as much as what it finds,
and abstention becomes a scoreable skill rather than a failure to answer.

---

## 2. Design

### 2.1 Scenarios

32 scenarios, hand-authored, in three families:

| Family | n | Question asked |
|---|--:|---|
| continuity | 14 | Can a cold agent resume work another agent stopped? |
| durability | 3 | Does the memory survive the cache being deleted? |
| memory | 15 | Classical recall, plus supersession, negation, abstention, arithmetic, temporal reasoning |

Each scenario is a sequence of timestamped events (checkpoints, working notes,
memories, vault notes) followed by one query with a token budget. Timestamps
are real and backdated, so "thirteen days ago" is thirteen days before the run
and staleness is genuinely measurable.

Scenarios are written adversarially, and **deliberately include cases the
author expected `brain` to fail**: 6 of the 32 are labelled known weaknesses in
the suite itself. The harness prints every case where the label mispredicted the
outcome, in either direction, so a scenario quietly becoming easy is visible.

### 2.2 Metrics

Every metric is computed by string matching against gold labels. No model
judges any output, which makes the suite deterministic and reproducible.

- **recall** — required facts present. Returns 1 when a scenario requires
  nothing, so abstention cases are not penalised for correctly carrying nothing.
- **leak** — facts present that should have been suppressed: superseded values,
  cancelled plans, another project's dead ends.
- **signal** — required *labels* present: "this is stale", "these two sources
  disagree", "I have no record of this". A system can carry every fact and score
  zero here.
- **fidelity** — `recall × (1 − leak)`. The headline quality number.
- **pass** — the scenario met **every** bar it set, signal labels included. This
  is deliberately harsh, and it is the number to read.
- **tokens / density** — mean response size, and required facts per 1000 tokens.

An early version scored fidelity only, and a staleness scenario scored 100% by
returning a two-week-old plan with no warning — doing precisely what the case
was written to catch. `pass` exists because of that.

One methodological trap is worth naming. `brain`'s renderer opens with
`# Context for: <task>`, which echoes the question. An early harness scored that
echo as a hit and gave a scenario 100% for returning two undated facts. The
scorer now strips any echo of the prompt before matching, and a regression test
(`TestTheQuestionCannotAnswerItself`) holds that line.

### 2.3 Systems

Nine systems. All embeddings are `nomic-embed-text` via Ollama, on the same
machine, so the comparison is like-for-like.

**Third-party, run for real:**

| System | Version | How it was driven |
|---|---|---|
| mem0 | 2.0.19 | `add(infer=False)`, `search(top_k=20)`, Qdrant local |
| MemPalace | 3.8.0 | native API, local store |
| Letta (MemGPT) | 0.16.8 | archival memory: `passages.create` / `passages.search(top_k=20)` |

Each runs as a subprocess in its own virtualenv speaking newline-delimited JSON,
so what is measured is their retrieval rather than a reimplementation of it.

**Controlled baselines**, which exist to make the third-party numbers legible:

- **full-dump** — return everything, budget permitting. The upper bound on recall
  and the lower bound on discipline.
- **recency-window** — return the most recent events.
- **vector-rag** — plain cosine top-k over the same embedder. The honest floor
  for "just use a vector database".
- **static-file** — a hand-written `CLAUDE.md`-style project file that never
  updates. What most teams actually do today.
- **none** — no memory. Confirms the scenarios are not self-answering.

### 2.4 What is not measured, and why

Two of the three third-party systems have an LLM-mediated write path that is
switched off here.

mem0's `add(infer=True)` runs a model over every write to extract and reconcile
facts. Letta's full agent loop lets a model decide what enters core memory and
when to search archival. Both are those systems at their most capable. Both are
also one-or-more model calls per event, against scenarios writing up to 200
events, across 32 scenarios — days of local inference.

The direction of that bias must be stated plainly: **disabling inference is
favourable on retrieval** (nothing is lost to a small model's extraction) and
**unfavourable on reconciliation** (supersession and contradiction are exactly
what the inference path exists to handle). mem0's and Letta's weakest results
here are in the arm that was switched off. This is the caveat most likely to
matter if these numbers are cited.

---

## 3. Results

### 3.1 Overall

| system | pass | fidelity | recall | leak | signal | tokens | dens/1k |
|---|--:|--:|--:|--:|--:|--:|--:|
| **brain** | **81.2%** | **82.8%** | **89.1%** | **33.3%** | **88.9%** | 253 | 6.5 |
| mempalace | 46.9% | 71.9% | 82.8% | 58.3% | 22.2% | 308 | 4.0 |
| recency-window | 46.9% | 68.8% | 84.4% | 83.3% | 22.2% | 230 | 11.0 |
| full-dump | 46.9% | 68.8% | 84.4% | 83.3% | 22.2% | 264 | 10.9 |
| letta | 43.8% | 67.2% | 82.8% | 83.3% | 22.2% | 169 | 11.2 |
| mem0 | 43.8% | 67.2% | 82.8% | 83.3% | 22.2% | 169 | 11.2 |
| vector-rag | 43.8% | 67.2% | 82.8% | 83.3% | 22.2% | 96 | 12.9 |
| static-file | 6.2% | 22.7% | 22.7% | 0.0% | 0.0% | 14 | 6.4 |
| none | 0.0% | 6.2% | 6.2% | 0.0% | 0.0% | 0 | 0.0 |

### 3.2 The finding that matters most: recall is not the differentiator

Every system that returns anything scores **82.8–89.1% recall**. The spread is
six points. On a conventional recall benchmark these systems are
indistinguishable.

The spread on **pass** is 43.8% to 81.2%, and on **leak** it is 33.3% to 83.3%.
The differentiation is entirely in what a system *suppresses* and *flags*, not
in what it finds.

This is the argument for the benchmark existing. A suite that measured only
recall would conclude that a plain vector database is as good as anything else —
and on recall, it is.

### 3.3 Universal blind spots

Eight skills scored **0% across every system in the field** when this suite was
first run:

`abstention` · `arithmetic` · `attribution` · `conflict` · `recency-conflict` ·
`scope` · `staleness` · `supersession`

These are not implementation bugs. They are a category-level gap: no system in
the field distinguished a superseded value from a current one, said who wrote
something, noticed that a note was two weeks old, or declined to answer when it
held nothing relevant.

Three remain unsolved by every system including `brain`:

- **arithmetic** (0% for all nine) — aggregating values across retrieved facts.
  Retrieval systems retrieve; none of them compute.
- **recency-conflict** (0% for all nine) — two sources disagree *and* one is
  newer, requiring both the conflict and the ordering to be surfaced.
- **temporal** — only MemPalace scores here (50%), and `brain` scores 0%.
  MemPalace's explicit time model earns it the one result no other system gets.

### 3.4 mem0 and Letta are the same system at this layer

mem0 and Letta return **byte-identical output** on every scenario — same pass,
fidelity, leak, signal, and the same 169 mean tokens. This was verified directly
outside the harness: given four events and one query, the two produce the same
string.

The explanation is not a harness bug. With inference disabled, both are a
verbatim passage store ranked by cosine similarity over the same embedding
model, returning the same top-k, packed to the same budget by the same rule.
Same corpus, same embedder, same ranking, same packer, same output.

The implication is worth stating: **the retrieval substrate of these products is
commodity.** Whatever distinguishes them lives in the write path — extraction,
reconciliation, what the agent chooses to store — which is precisely the part
this run switched off. Their convergence here is a fact about the layer being
measured, not a judgement of the products.

`vector-rag` — 40 lines of cosine top-k — matches both on pass and fidelity,
and beats both on density.

### 3.5 Where brain wins, and what it costs

`brain` holds six skills outright that no other system scores on: `staleness`,
`supersession`, `abstention`, `attribution`, `scope`, `died-mid-task`, plus
`source-of-truth` (100% vs 33% for static-file) and `conflict` (50% vs 0%).

It is also the only system scoring above 0% on the **durability** family (100%
vs 0% for everything except static-file's 33%) — the cases that delete the
index and require the memory to survive. Most systems have no answer because
their store *is* the database.

The cost is legibility of the win: these are the skills `brain` was modified to
address after this suite exposed them. See §5.

`brain` is not the densest system (6.5 facts/1k tokens against vector-rag's
12.9). Density bought by returning nothing but nearest neighbours is not free —
it is paid for in the leak column, where vector-rag sits at 83.3% against
`brain`'s 33.3%.

### 3.6 The static-file baseline

`static-file` scores 6.2%. It is included because it is what most teams do
today: a hand-written project file, committed once, never updated. It has 0%
leak — it cannot leak, because it never learns anything — and 22.7% recall.

---

## 4. Reproducing

```sh
git clone https://github.com/Coder8124/brain && cd brain
go build -o bin/brain ./cmd/brain
./bin/brain bench continuity              # whole field
./bin/brain bench continuity --brain-only # no Python systems needed
```

Third-party systems are auto-discovered: each `bench/adapters/*_adapter.py`
that reports itself runnable is included, and one that cannot import its own
package is **skipped rather than scored zero** — a missing row is honest where a
row of zeros would be a lie about that system's quality.

Letta additionally needs PostgreSQL with pgvector and a running `letta server`;
`bench/README.md` documents the setup.

**Environment for this run:** Apple M4 Pro, 48 GB, macOS 26.6.2, Go 1.26.5,
Ollama serving `nomic-embed-text` (768-dim). Raw output is retained per run.

---

## 5. Threats to validity

Listed in descending order of how much they should worry a reader.

1. **The benchmark author is the author of the leading system.** This is not
   independent evaluation and should not be read as such. Three mitigations are
   built in and none of them fully answers the objection: 6 of 32 scenarios are
   labelled as expected failures for `brain`; the harness prints every
   mislabelled prediction; and every fix is behavioural rather than
   case-specific (see below).

2. **Six of the eight universal blind spots were fixed in `brain` after this
   suite found them.** The baseline run had `brain` at 50.0%, one scenario ahead
   of the field. The current 81.2% reflects work motivated by these results.
   Every other system's score is unchanged across both runs, which is the
   control: nothing in the harness moved, only `brain` did. Whether that is
   "the benchmark drove real improvement" or "the system was fitted to the
   benchmark" is a fair question. The evidence for the former is that each fix
   changed behaviour visible on scenarios other than the one that exposed it —
   but the reader should weigh that claim knowing who is making it.

3. **The inference-disabled caveat (§2.4).** mem0 and Letta are run without
   their LLM write path. Their weakest categories are the ones that path exists
   to serve.

4. **32 scenarios.** One case moves the headline by roughly 3 points. The
   confidence intervals on any single skill (1–3 cases each) are wide enough
   that per-skill numbers should be read as direction, not measurement.

5. **Budgets are generous** relative to scenario size, which is why `full-dump`
   still reaches 46.9%. A tighter budget would separate the field further and
   would also make the suite a test of compression rather than of selection.

6. **Single embedding model, single machine, single run.** No seed variance is
   reported. Deterministic scoring removes judge variance but not embedding or
   ordering variance.

7. **English-only, software-project-shaped scenarios.** The vocabulary is
   hardware and software product work because that is what the author could
   write adversarially with confidence.

---

## 6. What would make this stronger

Honest next steps, roughly by value:

- **Independent replication**, ideally by someone who did not write `brain`.
- **A held-out suite** authored by someone else against the same rubric, which
  is the only clean answer to threat (2).
- **mem0 with `infer=True` and Letta with its agent loop**, at whatever scale is
  affordable, to close threat (3).
- **More scenarios**, particularly in the three unsolved skills, where n is
  currently 1–2.
- **Seed and model variance**: repeat under two or three embedding models.

---

## 7. Related work

LongMemEval and LoCoMo measure long-horizon conversational recall; `brain`
scores 96.0% recall@5 / 99.2% recall@10 on the full 500-question LongMemEval-S,
which is a different and easier question than the one asked here. MemGPT/Letta
introduced the tiered context model this benchmark's archival arm exercises.
mem0 and MemPalace are contemporaneous memory layers with published claims on
recall benchmarks. None of these evaluate handoff between agents, which is the
gap this suite addresses.

---

## Appendix: pass rate by skill

| skill | brain | mempalace | letta | mem0 | vector-rag | full-dump | recency | static |
|---|--:|--:|--:|--:|--:|--:|--:|--:|
| abstention | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| arithmetic | 0% | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| attribution | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| budget | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| cold-start | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| conflict | **50%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| died-mid-task | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| distractors | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| graph-reach | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 100% |
| lexical | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| multi-hop | 0% | 0% | 0% | 0% | 0% | **100%** | **100%** | 0% |
| multi-hop-handoff | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| negation | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| negative-knowledge | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| open-questions | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| preference | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| rationale | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| recall | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| recency-conflict | 0% | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| scale | 100% | 100% | 100% | 100% | 100% | 100% | 100% | 0% |
| scope | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| source-of-truth | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 33% |
| staleness | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| supersession | **100%** | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| temporal | 0% | **50%** | 0% | 0% | 0% | 0% | 0% | 0% |

Note `multi-hop`, where `brain` scores 0% and the two dump-everything baselines
score 100%: chaining two facts is easier when you were handed all of them. That
case is in the suite because it is a real cost of selection, and it is one of
the six labelled expected failures.

**Family pass rates**

| system | continuity | durability | memory |
|---|--:|--:|--:|
| brain | 86% | 100% | 73% |
| mempalace | 50% | 0% | 53% |
| recency-window | 50% | 0% | 53% |
| full-dump | 50% | 0% | 53% |
| letta | 50% | 0% | 47% |
| mem0 | 50% | 0% | 47% |
| vector-rag | 50% | 0% | 47% |
| static-file | 0% | 33% | 7% |
| none | 0% | 0% | 0% |
