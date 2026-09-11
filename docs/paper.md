# Handoff, Not Recall: Measuring What Agent Memory Systems Lose Between Sessions

*Working draft, 2026-08-30. Not submitted anywhere.*

---

## Abstract

Benchmarks for agent memory measure retrieval: given a long history, can the
system find the fact that answers a question. We argue this measures the wrong
thing for the setting memory systems are actually deployed in — one agent stops
mid-task and a different agent, often a different product, continues. We
characterise this **handoff** setting and identify four capabilities it requires
that recall benchmarks structurally cannot test: negative knowledge (what was
ruled out), supersession (what has since changed), provenance and staleness (who
said it, and when), and abstention (saying nothing is on record). We add a
fifth, orthogonal to retrieval: durability under cache loss.

We present **CONTINUITY-32**, a suite of 32 hand-authored scenarios scored on
four axes simultaneously — carry, leak, signal, and token budget — where a
scenario passes only if it clears every bar. We evaluate nine systems (four real
memory systems, five controls) locally on identical hardware and a shared
embedding model.

The headline finding is that **retrieval is not the differentiator**. Every real
system carries between 82.8% and 89.1% of required facts. The spread in overall
pass rate (81.2% to 43.8%) comes almost entirely from *leakage* — returning
superseded values alongside current ones — and from *signal*, the framing that
tells a receiving agent something was already tried or has since changed. On
signal, one system scores 88.9% and the rest score 22.2%; on durability under
cache deletion, one scores 100% and the rest score 0%.

We report where the best system loses, including one skill where a competitor
beats it outright, and two where the entire field scores zero. **The suite was
authored by the same team as the highest-scoring system.** We treat this as a
first-order threat to validity and structure the paper so the claim is
falsifiable rather than asking the reader to trust it.

---

## 1. Introduction

An agent memory system is usually evaluated by asking it a question. LongMemEval,
LoCoMo and the retrieval suites in their lineage all share a shape: construct a
long conversational history, ask something answerable from it, measure whether
the retrieved context contains the answer.

This is a real capability and it is close to saturated. The system described here
scores 96.0% recall@5 on the full 500-question LongMemEval-S, and competing
systems score comparably. When a benchmark no longer separates the field, it has
stopped measuring the thing that is hard.

Meanwhile the deployment setting has moved. Memory systems are increasingly
installed as protocol servers — under the Model Context Protocol — serving
several agent products at once from a single store on a developer's machine. The
question they are asked is no longer "what did the user say about X" but
something closer to:

> *Another agent worked on this yesterday. What do I need to know before I touch
> it?*

We call this the **handoff** setting, and it is not a longer version of recall.

### 1.1 Why handoff is a different problem

Four capabilities separate handoff from retrieval, and no recall benchmark tests
any of them, because each is a property of a *plan* rather than an *answer*:

1. **Negative knowledge.** The most expensive thing an agent learns in a session
   is what did not work. It is not the answer to any question — it is a
   constraint on the next agent's search. Recall benchmarks have no slot for it.

2. **Supersession.** Histories contain prices that changed, plans that were
   cancelled, decisions reversed. Returning the stale value *alongside* the
   current one scores as a hit under recall metrics, while in practice handing
   the receiving agent a coin flip is worse than handing it nothing.

3. **Provenance and staleness.** Who recorded a claim and when determines whether
   acting on it is reasonable. A retrieved sentence carrying neither is a
   liability, and retrieval metrics are indifferent to both.

4. **Abstention.** When nothing on record bears on the question, the correct
   output is to say so. Nearest-neighbour retrieval structurally cannot do this:
   there is always a nearest neighbour, and its cosine score carries no
   information about whether it is relevant in absolute terms.

We add a fifth family that concerns the substrate rather than the retrieval:

5. **Durability.** Does what the system knows survive deletion of its own
   derived state? Systems that describe an index as a "rebuildable cache" make a
   testable claim, and it is rarely tested.

### 1.2 Contributions

- A characterisation of the handoff setting and why recall benchmarks cannot
  measure it.
- **CONTINUITY-32**, a 32-scenario suite with a conjunctive four-axis metric
  that penalises dumping history, released with the harness and adapters.
- An evaluation of nine systems on identical local hardware, isolating
  *leakage* and *signal* as the axes on which the field actually differs.
- A negative-results section: two families where every system scores 0%, one
  where the best system scores 0% and a competitor scores 50%.

---

## 2. Related work

**Recall benchmarks.** LongMemEval and LoCoMo evaluate long-history question
answering. Both are well-constructed for what they measure and neither includes
a scenario in which the correct output is "this was tried and abandoned" or
"nothing on record covers that".

**Memory systems.** Recent systems fall into three architectural families:
extraction-and-consolidation stores (Mem0), temporal knowledge graphs (Zep /
Graphiti), and agent-managed context hierarchies in the MemGPT tradition
(Letta). Cross-tool protocol servers built on MCP are a fourth, more recent
group. Reported comparisons between these are typically on recall benchmarks,
which — per §1 — do not separate them on the axes we find decisive.

**Portability.** A 2026 industry survey notes that no standard schema exists for
what a "memory" is, and that switching memory providers is consequently as
painful as switching agents. Durability under cache loss (§5.4) is a weak but
measurable proxy for that concern: a system whose knowledge exists only inside
its own store cannot be migrated out of it.

---

## 3. Task formulation

A scenario is a triple ⟨E, q, R⟩:

- **E** — an ordered event stream written into the system under test through its
  own public API. Events carry timestamps, an author, and a project scope.
  Backdating is real: an event dated thirteen days ago *is* thirteen days old,
  so staleness scenarios are not simulated.
- **q** — a query issued at a later time, phrased as an arriving agent would
  phrase it.
- **R** — a rubric, described below.

Systems are invoked through a uniform adapter protocol: each receives the same
events in the same order and answers the same query within the same token
budget. Adapters translate to each system's native API and are roughly 100 lines
each.

### 3.1 The rubric

Recall alone rewards returning everything. So each scenario scores four axes:

| Axis | Definition |
|---|---|
| **carry** | fraction of required facts present in the output |
| **leak** | fraction of forbidden items present — superseded values, other projects' dead ends, cancelled plans |
| **signal** | required framing present — "this was tried", "this changed", "nothing on record" |
| **tokens** | output size against the scenario's budget |

We report **fidelity** = carry × (1 − leak) as a summary, but the primary metric
is **pass**, which is conjunctive: a scenario passes only if carry, leak *and*
signal all clear their thresholds.

The conjunction is the design. A system that returns the correct current price
*and* the superseded one it replaced has not answered the question. Any metric
that averages across axes would score that as partial credit; in the handoff
setting it is a failure, because the receiving agent cannot tell which number to
act on.

---

## 4. Experimental setup

Nine systems: four real, five controls.

| System | Description |
|---|---|
| **brain** | markdown checkpoints, hybrid BM25+vector retrieval with RRF fusion, budgeted context assembly |
| **letta** | Letta 0.16.8 (formerly MemGPT), archival memory, local server |
| **mem0** | mem0ai, verbatim store with BM25/vector search |
| **mempalace** | MemPalace, local spatially-scoped store |
| *vector-rag* | control: embed everything, return top-k by cosine |
| *recency-window* | control: return the last N events |
| *full-dump* | control: return everything, truncated to budget |
| *static-file* | control: a hand-written project file, never updated |
| *none* | control: no memory. The floor. |

The controls are load-bearing. Without *full-dump* it is impossible to know
whether a system's pass rate reflects judgement or merely a generous budget;
without *none* the scale has no zero.

Every system runs on one M-series Mac, embeds through the same local Ollama with
`nomic-embed-text`, and makes no network calls. An adapter that cannot import
its own package is **skipped rather than scored zero** — a missing row is honest
where a row of zeros would be a false claim about that system.

---

## 5. Results

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

### 5.1 Retrieval is not the differentiator

Every real system carries between 82.8% and 89.1% of required facts — a spread
of 6.3 points, within noise for a 32-scenario suite. Meanwhile pass rates span
37.4 points. **Whatever separates these systems, it is not their ability to find
the relevant passage.**

### 5.2 Leakage is

The best system leaks 33.3%; every embedding-based system except MemPalace leaks
83.3%. The mechanism is direct: a superseded price and its replacement are
near-identical in embedding space, both rank highly for the same query, and
nothing in a cosine score encodes *"this one was replaced"*. Supersession is not
a retrieval property, so retrieval-shaped systems do not have it.

### 5.3 Signal is a cliff, not a slope

88.9% versus 22.2%, with nothing in between. No system in the field except one
says "this was already tried", "this value changed", or "nothing on record
covers that". This is not a tuning gap — it is a category these systems do not
model. A retrieval system returns passages; framing is not a passage.

### 5.4 Durability

Three scenarios write, delete every rebuildable artifact, and read again. One
system scores 100%; every other system, including every control, scores 0%.

This measures where the source of truth lives rather than retrieval quality.
brain writes to markdown files the user owns and treats its SQLite index as a
cache; the others keep knowledge inside their own store, so deleting the store
deletes what they know.

We note this family exists because **it was once false for brain too**: memories
lived only in the cache, and the project's own documentation instructed users to
delete it. The benchmark caught it. We report this because a family that only
ever passed would be evidence of nothing.

### 5.5 An identical row is a harness check

Letta and mem0 score identically across every axis at identical mean token
count. With extraction disabled both reduce to the same algorithm — store event
text verbatim, embed with `nomic-embed-text`, return top-k by cosine within
budget. Same corpus, same embedding model, same ranking, therefore the same
passages. The identical row is evidence the harness is doing what it claims, and
a reminder that for those two systems what is being measured is their retrieval
substrate, not their agent loops (see §7).

### 5.6 Density is a trap

*vector-rag* achieves the highest fact density (12.9 per 1000 tokens) and passes
43.8%. It buys density by returning nothing but nearest neighbours: no
provenance, no ordering, no framing. Efficiency purchased at the cost of
everything that makes retrieved context actionable.

---

## 6. Negative results

Reported because a benchmark that shows only wins is marketing.

| Family | Result |
|---|---|
| **arithmetic** | **0% for every system.** Aggregating values across records. Retrieval is not computation. |
| **recency-conflict** | **0% for every system.** Two sources disagree *and* one is newer; preferring recency unprompted. |
| **temporal** | **brain 0%, MemPalace 50%.** Ordering events and answering windowed questions. The one skill where a competitor wins outright. |
| **multi-hop** | **brain 0%; recency-window and full-dump 100%.** The dumb controls win by carrying everything — the tradeoff their 83.3% leak pays for, but a loss regardless. |
| **conflict** | **brain 50%.** Half the contradiction cases remain unflagged. |

The temporal result is corroborated externally: an independent comparison
reports Zep's temporal knowledge graph scoring ~15 points higher than
alternatives on LongMemEval temporal reasoning. Two unrelated sources agreeing
raises our confidence that this is a genuine architectural gap rather than a
suite artifact — interval-valued facts (`valid_from` / `valid_until`) are absent
from the system, and that absence is visible.

---

## 7. Threats to validity

**The suite was written by the authors of the highest-scoring system.** This is
the dominant threat and no amount of care removes it. What we have done instead:

- Six scenarios are labelled as known weaknesses *before* the run, and the
  harness prints every wrong prediction rather than only successes.
- Every scenario carries a `Why` line stating what it is really asking, so a
  reader can judge whether the framing is fair rather than inferring it from the
  score.
- Five controls, including a full-dump baseline that passes 46.9%, bound how
  much credit the metric gives for judgement over volume.
- §6 exists.

None of this makes the evaluation independent. **It should not be read as
independent**, and the strongest use of these numbers is as a falsifiable claim
someone else can test with the released harness.

**Competing systems ran with extraction disabled** — `mem0` with `infer=False`,
Letta with its agent loop off. This is favourable to them on retrieval, since
nothing is lost to a small model's extraction, and unfavourable on
reconciliation, which is exactly where their headline weakness (supersession)
sits. **This is the caveat most likely to change conclusions.** Running either
authentically requires one or more LLM calls per event, and a single scenario
writes upward of two hundred events. We consider a properly-resourced rerun with
extraction enabled the single most valuable follow-up.

**32 scenarios.** One case moves the headline by ~3 points. Differences under ~6
points should be treated as noise, which specifically means the 46.9%/43.8%
cluster is one group, not a ranking.

**Budgets are generous relative to scenario size**, which is why full-dump still
reaches 46.9%. A tighter budget would separate the field further and would be a
different benchmark.

**Single machine, single embedding model, single run.** No variance across
hardware, seeds, or embedding choice is reported. Systems differ in operational
weight in ways the numbers do not capture: Letta alone requires PostgreSQL with
pgvector and a running server.

---

## 8. Reproducibility

Harness, scenarios, scoring and adapters are released with the system.

```sh
go run ./cmd/brain bench continuity --brain-only   # needs only Ollama
go run ./cmd/brain bench continuity                # the full field
go run ./cmd/brain bench continuity list           # every scenario and what it asks
```

Scenario definitions are in `internal/eval/scenarios.go`, scoring in
`internal/eval/score.go`, adapters in `bench/adapters/`. Each adapter is ~100
lines translating harness events into one system's API.

---

## 9. Conclusion

Agent memory is evaluated on recall and deployed for handoff. On a suite built
for handoff, retrieval quality does not separate the field — every real system
finds the relevant passage — while leakage and framing separate it by 37 points.
The capabilities that matter are supersession, provenance, abstention, and
saying what was already ruled out; the systems we measured largely do not model
them, and one of them cannot survive deletion of its own cache.

We do not claim the system that scores best here is the best memory system. We
claim the axis the field is being compared on is the wrong one, and offer a
falsifiable instrument for comparing it on a better one.

---

## Appendix A — Where the difference came from

Eight skills scored 0% across the entire field in the first run of this suite.
Seven were subsequently held by one system with no other system moving. Each
change was behavioural rather than suite-fitting, and each shows up on scenarios
other than the one that exposed it:

| Change | Skills affected |
|---|---|
| Age and author on every uncommitted note, with an explicit warning past seven days | staleness, attribution |
| Two-tier checkpoint budget — decisions, dead ends and next step charged before the session log | distractors |
| Predecessors' ruled-out approaches carried forward across handoffs, attributed | multi-hop-handoff |
| Supersession at recall: later value wins, earlier dropped, reported as "changed" without reprinting the dead value | supersession |
| Cancelled plans suppressed — a withdrawn next step replaced by the decision that withdrew it | superseded-plan |
| Contradictions flagged rather than resolved when no ordering exists | conflict |
| Abstention when nothing retrieved is on topic | abstention |

## Appendix B — Open problems

1. **Interval-valued facts.** `valid_from` / `valid_until` on records, to close
   the temporal family. Externally corroborated as a real gap (§6).
2. **Aggregation over retrieved records**, to make the arithmetic family
   non-vacuous — or an argument that it belongs to the agent, not the memory.
3. **Recency as a default tiebreak** for the recency-conflict family.
4. **Independent replication** with extraction enabled on competing systems.
5. **A portable interchange format.** Durability is currently measured as "does
   it survive cache deletion"; the stronger property is whether a memory can be
   moved between systems at all.
