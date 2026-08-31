# Handoff, Not Recall: Measuring What Agent Memory Systems Lose Between Sessions

*Working draft, 2026-08-30. Not submitted anywhere.*

---

## Abstract

Benchmarks for agent memory are built around question answering: given a long
history, can the system retrieve the fact that answers a question. We argue that
this leaves a gap for the setting memory systems are increasingly deployed in —
one agent stops mid-task and a different agent, often a different product,
continues. We characterise this **handoff** setting: the successor needs the
facts that bear on its task, needs superseded and out-of-scope material actively
suppressed, needs to be told what was already tried and what has since changed,
and needs all of it inside a token budget it did not choose.

Existing suites test several of the component capabilities — LongMemEval scores
knowledge updates, temporal reasoning and abstention as named abilities — but
score them one at a time, as answer correctness, without a penalty for what else
the context carries. We contribute a task formulation that composes them:
a single output judged simultaneously on carry, leak, signal and size, where a
scenario counts only if it clears every bar at once.

We present **CONTINUITY-32**, 32 hand-authored scenarios in that form, and
evaluate nine systems (four memory systems, five controls) locally on identical
hardware and a shared embedding model. On this suite, required-fact carry varies
far less than pass rate: every real system carries 82.8–89.1% of required facts,
while pass rates span 43.8% to 81.2% (95% Wilson intervals [28.2, 60.7] and
[64.7, 91.1]; the separation survives the worst admissible paired split at
p < 0.035). The variance that pass rate picks up and carry does not is
concentrated in *leakage* — returning superseded values alongside current ones —
and in *signal*, the framing that tells a successor something was already tried
or has since changed. On signal, one system scores 88.9% and the rest 22.2%; on
durability under cache deletion, one scores 100% and the rest 0%.

We report where the best system loses, including one skill where a competitor
beats it outright and two where the entire field scores zero. **The suite was
authored by the same team as the highest-scoring system, and every scenario in
it was written during that system's development.** We treat this as a
first-order threat that disclosure does not fix, state precisely which claims it
does and does not permit, and structure the paper so the claim is falsifiable
rather than asking the reader to trust it. These are a first measurement, not a
ranking.

---

## 1. Introduction

An agent memory system is usually evaluated by asking it a question. LongMemEval,
LoCoMo and the suites in their lineage share a shape: construct a long
conversational history, ask something answerable from it, measure whether the
answer is right.

This is a real capability and on the retrieval axis it is close to saturated. The
system described here scores 96.0% recall@5 on the full 500-question
LongMemEval-S, and competing systems score comparably. When a benchmark stops
separating a field, it has stopped measuring the thing that is hard.

Meanwhile the deployment setting has moved. Memory systems are increasingly
installed as protocol servers — under the Model Context Protocol — serving
several agent products at once from a single store on a developer's machine. The
request they receive is no longer "what did the user say about X" but something
closer to:

> *Another agent worked on this yesterday. What do I need to know before I touch
> it?*

We call this the **handoff** setting. It is not a longer version of recall, and
the difference is not that its component skills are unheard of. It is that the
output is a *briefing* rather than an *answer*, and a briefing is judged by what
it includes, what it omits, what it flags, and how much of the successor's window
it costs — all at once.

### 1.1 What changes when the output is a briefing

Four properties of the handoff output are poorly served by answer scoring. Two
of them prior work names as abilities and tests directly; what is missing in
those cases is not the capability but a metric under which the *other* things the
response carries cost anything. The other two have no slot in an answer-scored
benchmark at all.

1. **Negative knowledge.** The most expensive thing an agent learns in a session
   is what did not work. It is not the answer to any question — it is a
   constraint on the next agent's search. Under answer scoring there is no slot
   for it: no question has "this was tried and abandoned" as its gold response.

2. **Supersession.** Histories contain prices that changed, plans that were
   cancelled, decisions reversed. LongMemEval scores knowledge updates by asking
   for the current value, and a system that emits the current value *and* the one
   it replaced answers correctly. In a briefing that is a coin flip handed to the
   successor, and worse than saying nothing. The capability is tested; the
   leakage is not penalised.

3. **Provenance and staleness.** Who recorded a claim and when determines whether
   acting on it is reasonable. Correctness metrics are indifferent to both: a
   retrieved sentence that is right and undated scores identically to one that
   carries its author and age.

4. **Abstention.** When nothing on record bears on the question, the correct
   output is to say so. LongMemEval scores this directly, and it remains hard for
   nearest-neighbour retrieval for a structural reason: there is always a nearest
   neighbour, and its cosine score carries no information about relevance in
   absolute terms. We inherit the capability and add the constraint that
   abstention must survive a budget in which returning *something* is cheap.

We add a fifth family that concerns the substrate rather than the retrieval, and
which we have not found tested anywhere:

5. **Durability.** Does what the system knows survive deletion of its own derived
   state? Systems that describe an index as a "rebuildable cache" make a testable
   claim, and it is rarely tested.

The claim of this paper is not that these capabilities are unknown to prior work.
It is that scoring them individually, as answer correctness, does not predict
whether a system produces a usable handoff — and §5 is the evidence: on this
suite the systems agree almost exactly on what they can find and disagree by 37
points on what they hand over.

### 1.2 Contributions

- A formulation of the handoff task: a successor-facing briefing scored
  conjunctively on carry, leak, signal and budget, distinguished from the
  answer-scored recall task it is usually conflated with.
- **CONTINUITY-32**, a 32-scenario suite in that form, released with harness,
  scoring and adapters.
- An evaluation of nine systems on identical local hardware which finds, *on this
  suite*, that carry varies far less than pass rate, and that leakage and signal
  account for most of the observed separation.
- A negative-results section: two families where every system scores 0%, one
  where the best system scores 0% and a competitor 50%.
- An explicit statement of what the authorship of the suite does and does not
  permit us to claim, and a concrete plan (§7.1) — held-out and externally
  authored scenario sets, authentic competitor configurations, ablations, and a
  downstream utility study — for the experiments that would license the stronger
  claims.

---

## 2. Related work

**Recall benchmarks.** LongMemEval evaluates long-history question answering
across five named abilities: information extraction, multi-session reasoning,
temporal reasoning, knowledge updates, and abstention. LoCoMo covers long-term
conversational QA, event-graph summarisation, and multimodal dialogue generation.
Both are well constructed, and both test capabilities we care about. The
difference is in the unit of judgement: in each, a scenario is scored on whether
a produced answer is correct. Neither penalises a response for also carrying the
superseded value, the other project's dead end, or four hundred tokens of
irrelevant history alongside the right answer, and neither asks for the framing
("this was already tried") that is not an answer to any question. Our suite is
not a replacement for either; it measures a different property of the same
systems.

**Memory systems.** Recent systems fall into three architectural families:
extraction-and-consolidation stores (Mem0), temporal knowledge graphs (Zep /
Graphiti), and agent-managed context hierarchies in the MemGPT tradition (Letta).
Cross-tool protocol servers built on MCP are a fourth, more recent group.
Reported comparisons between these are typically on recall benchmarks, which —
per §1 — do not score them on the axes we find decisive here.

**Portability.** A 2026 industry survey notes that no standard schema exists for
what a "memory" is, and that switching memory providers is consequently as
painful as switching agents. Durability under cache loss (§5.4) is a weak but
measurable proxy for that concern: a system whose knowledge exists only inside
its own store cannot be migrated out of it.

**Terminology.** "Memory system" is used in the literature for at least five
distinct things: a persistence layer, a retrieval engine, a reconciliation layer
that decides which of several recorded claims is current, a handoff generator
that renders selected records as prose for another agent, and a whole product
comprising all four plus an agent loop. This paper uses **memory-and-handoff
system** for a stack that performs all of the first four, and is explicit in §3.2
about which layers each measured configuration exposes.

---

## 3. Task formulation

A scenario is a triple ⟨E, q, R⟩.

**E — the event stream.** An ordered sequence written into the system under test
through its own public API. Events carry timestamps, an author, and a project
scope. Backdating is real: an event dated thirteen days ago *is* thirteen days
old, so staleness scenarios are not simulated.

**q — the handoff query.** Not a question. A query specifies the successor's
situation, and all six fields are supplied to every system identically:

| Field | Meaning |
|---|---|
| task | what the arriving agent is about to do |
| project | the scope that is in bounds; everything else is out |
| now | the wall-clock time the query is issued, against which staleness is judged |
| budget | tokens available for the briefing |
| audience | model, human, or both — fixed to *model* throughout this suite |
| forbidden | what counts as leakage for this scenario (see R) |

So the queries read as *"You are taking over the deployment issue on Project A.
What should you know before attempting another fix?"* rather than *"what happened
with the deployment?"*

**R — the rubric.** Four label sets per scenario: required facts, forbidden
items, required signals, and a token budget.

### 3.1 Metric hierarchy

There are more numbers in §5 than a reader should have to hold. The hierarchy is:

**Primary — pass**, a conjunction. A scenario passes when

- fidelity = carry × (1 − leak) ≥ 0.75, **and**
- signal ≥ 0.75, where the scenario declares any required signals, **and**
- the adapter returned without error.

**Secondary — the axes the conjunction is built from:** carry (fraction of
required facts present), leak (fraction of forbidden items present), signal
(fraction of required framings present), and tokens (mean output size against
budget).

**Reported separately, not gated:** budget overruns. A response that exceeds its
budget is flagged and its token count reported, but does not fail the scenario on
that ground alone. This is a deliberate weakening — a budget-gated variant is a
different and stricter benchmark, and we do not report one.

**Diagnostic:** fact density per 1000 tokens, and the per-family and per-skill
breakdowns. Density in particular is a diagnostic and not a goal; §5.6 shows the
system that maximises it is not the system that passes.

Two conventions matter for reading the numbers. A scenario with no required facts
scores carry = 1 rather than 0: the abstention cases have no facts to fetch by
design, and dividing zero by zero as a miss penalised systems for correctly
declining to invent one. And the query string is stripped from the output before
matching, because systems that echo their prompt would otherwise satisfy any gold
label whose wording overlaps the question.

**On the 0.75 bar.** A single threshold is used for both fidelity and signal, and
it was fixed before the comparative run but *not* before the suite's own
development, during which the authors' system was iterated against it. We report
sensitivity to this choice as an open item (§7.1); readers should treat 0.75 as
an author-chosen bar, not a pre-registered one.

**Why conjunctive.** A system that returns the correct current price *and* the
superseded one it replaced has not produced a usable briefing. Any metric that
averages across axes scores that as partial credit; for a successor that must
pick a number to act on, it is a failure. The conjunction is what makes "dump
everything" lose, and its cost is that it is coarse — which is why the secondary
axes are reported alongside it rather than beneath it.

### 3.2 System boundary

CONTINUITY-32 evaluates a **pipeline**, not a component. What is measured for
each system is: its persistence layer, its retrieval, whatever reconciliation it
performs, and its rendering of selected records into an output — everything
between the write API and the returned string. Adapters (~100 lines each)
translate harness events into each system's native API and do no reconciliation,
reranking, filtering or rewriting of their own.

Two consequences follow, and we state them rather than let them be inferred:

- A failure observed here is a failure *of the configured pipeline*, and cannot
  be attributed to any one layer without an ablation. §7.1 lists the ablations
  that would make such attribution legitimate; we have not run them.
- The configurations are not uniform in how much of each product is exercised.
  For two systems, extraction is disabled, which reduces them to their retrieval
  substrate (§7). Where that is true we say so, and we do not present those rows
  as measurements of the products' full behaviour.

---

## 4. Experimental setup

Nine systems: four real, five controls.

| System | Description | Layers exercised |
|---|---|---|
| **brain** | markdown checkpoints, hybrid BM25+vector retrieval with RRF fusion, budgeted context assembly | persistence, retrieval, reconciliation, rendering |
| **letta** | Letta 0.16.8 (formerly MemGPT), archival memory, local server, **agent loop off** | persistence, retrieval |
| **mem0** | mem0ai, `infer=False`, verbatim store with BM25/vector search | persistence, retrieval |
| **mempalace** | MemPalace, local spatially-scoped store | persistence, retrieval, some reconciliation |
| *vector-rag* | control: embed everything, return top-k by cosine | retrieval |
| *recency-window* | control: return the last N events | — |
| *full-dump* | control: return everything, truncated to budget | — |
| *static-file* | control: a hand-written project file, never updated | — |
| *none* | control: no memory. The floor. | — |

The controls are load-bearing. Without *full-dump* it is impossible to know
whether a system's pass rate reflects judgement or merely a generous budget;
without *none* the scale has no zero.

Every system runs on one M-series Mac, embeds through the same local Ollama with
`nomic-embed-text`, and makes no network calls. An adapter that cannot import its
own package is **skipped rather than scored zero** — a missing row is honest
where a row of zeros would be a false claim about that system.

The right-hand column is the comparability caveat in table form. **brain is the
only row exercising a full memory-and-handoff pipeline.** Two rows are products
reduced to their retrieval substrates. This is discussed as a threat in §7 rather
than a footnote, because it is the caveat most likely to change the conclusions.

---

## 5. Results

```
system                pass         95% CI  fidelity  carry   leak  signal  tokens  dens/1k
brain                81.2%  [64.7, 91.1]     82.8%  89.1%  33.3%   88.9%     253      6.5
mempalace            46.9%  [30.9, 63.6]     71.9%  82.8%  58.3%   22.2%     308      4.0
recency-window       46.9%  [30.9, 63.6]     68.8%  84.4%  83.3%   22.2%     230     11.0
full-dump            46.9%  [30.9, 63.6]     68.8%  84.4%  83.3%   22.2%     264     10.9
letta                43.8%  [28.2, 60.7]     67.2%  82.8%  83.3%   22.2%     169     11.2
mem0                 43.8%  [28.2, 60.7]     67.2%  82.8%  83.3%   22.2%     169     11.2
vector-rag           43.8%  [28.2, 60.7]     67.2%  82.8%  83.3%   22.2%      96     12.9
static-file           6.2%   [1.7, 20.1]     22.7%  22.7%   0.0%    0.0%      14      6.4
none                  0.0%   [0.0, 10.7]      6.2%   6.2%   0.0%    0.0%       0      0.0
```

Intervals are Wilson 95% on n = 32. They are wide, and they are the reason no
ordering within the 46.9%/43.8% cluster should be read as a ranking: those rows
differ by a single scenario, and a paired test on them is not significant under
any admissible split of the discordant pairs.

The brain-to-field gap does survive a paired test. Scenarios are shared, so the
appropriate test is McNemar's on the discordant pairs. We do not publish the
paired table here, but the counts bound it: with 26/32 against 15/32, the
discordant pairs satisfy b − c = 11, and every admissible split gives
p ≤ 0.035 (worst case b = 17, c = 6); against 14/32, p ≤ 0.023. So the separation
between brain and the rest of the field is unlikely to be sampling noise on this
suite. That is a statement about this suite of 32 author-written scenarios, and
§7 is about why that is a much weaker statement than it sounds.

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

Families are 14 continuity, 15 memory, 3 durability scenarios. The durability
column rests on three cases and one binary architectural property; it should be
read as a demonstration that the property is testable and currently unmet, not as
a rate.

### 5.1 On this suite, carry does not separate the field

Every real system carries between 82.8% and 89.1% of required facts, a spread of
6.3 points on 32 scenarios — a difference a single scenario's worth of noise
covers three times over. Pass rates over the same rows span 37.4 points. **On
CONTINUITY-32, the ability to find the relevant passage is not what separates
these systems.**

We state this as a property of the suite. It is consistent with the recall
literature's saturation, but two things would be needed to make it a claim about
memory systems generally: scenarios not written by us (§7.1), and configurations
that exercise each product's full pipeline (§7). Neither is present here.

### 5.2 Leakage carries most of the separation

The best system leaks 33.3%; every embedding-based system except MemPalace leaks
83.3%. The mechanism is direct: a superseded price and its replacement are
near-identical in embedding space, both rank highly for the same query, and
nothing in a cosine score encodes *"this one was replaced"*. Supersession is a
reconciliation property rather than a retrieval one, and the configurations that
expose only retrieval do not exhibit it. Whether the two products whose
reconciliation we disabled would exhibit it when enabled is exactly the open
question in §7.

### 5.3 Signal is a cliff, not a slope

88.9% against 22.2%, with nothing in between. No configuration in this field
except one says "this was already tried", "this value changed", or "nothing on
record covers that". A distribution with no middle is more consistent with a
capability that is either implemented or absent than with a tuning gap: framing
is not a passage, so a system that returns passages has no way to emit it. The
22.2% floor is identical across seven unrelated configurations, which is what a
floor set by incidental wording overlap rather than by signalling behaviour would
look like; we have not verified that directly.

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

Letta and mem0 score identically across every axis at identical mean token count.
With extraction disabled both reduce to the same algorithm — store event text
verbatim, embed with `nomic-embed-text`, return top-k by cosine within budget.
Same corpus, same embedding model, same ranking, therefore the same passages. The
identical row is evidence the harness is doing what it claims, and simultaneously
the clearest statement of the comparability problem: for those two rows, what is
measured is a retrieval substrate we configured, not a product.

### 5.6 Density is a trap

*vector-rag* achieves the highest fact density (12.9 per 1000 tokens) and passes
43.8%. It buys density by returning nothing but nearest neighbours: no
provenance, no ordering, no framing. Efficiency purchased at the cost of
everything that makes retrieved context actionable. This is why density is
reported as a diagnostic and never as a headline.

---

## 6. Negative results

Reported because a benchmark that shows only wins is marketing. Each row rests on
two to three scenarios, so these are directional findings, not rates.

| Family | Result |
|---|---|
| **arithmetic** | **0% for every system.** Aggregating values across records. Retrieval is not computation. |
| **recency-conflict** | **0% for every system.** Two sources disagree *and* one is newer; preferring recency unprompted. |
| **temporal** | **brain 0%, MemPalace 50%.** Ordering events and answering windowed questions. The one skill where a competitor wins outright. |
| **multi-hop** | **brain 0%; recency-window and full-dump 100%.** The dumb controls win by carrying everything — the tradeoff their 83.3% leak pays for, but a loss regardless. |
| **conflict** | **brain 50%.** Half the contradiction cases remain unflagged. |

The temporal result is corroborated externally: an independent comparison reports
Zep's temporal knowledge graph scoring ~15 points higher than alternatives on
LongMemEval temporal reasoning. Two unrelated sources agreeing raises our
confidence that this is a genuine architectural gap rather than a suite artifact
— interval-valued facts (`valid_from` / `valid_until`) are absent from the
system, and that absence is visible.

The multi-hop row deserves emphasis in the other direction: it is the case where
the conjunctive metric's premise is most exposed. Dumping everything is the
right answer there, and our metric says so.

---

## 7. Threats to validity

**The suite was written by the authors of the highest-scoring system, during that
system's development.** This is the dominant threat and disclosure does not
remove it. It is also broader than scenario wording: the same authors control the
memory schema, the checkpoint format, the adapter boundary, the context
assembler, the output phrasing, the rubric labels, and the 0.75 bar. Seven of the
behaviours in Appendix A were built in response to scenarios in this suite. That
is the normal way a system improves against a benchmark, and it is also exactly
the process that makes a self-authored benchmark unreliable as a ranking.

What we have done, none of which makes the evaluation independent:

- Six scenarios are labelled as known weaknesses *before* the run, and the
  harness prints every wrong prediction rather than only successes.
- Every scenario carries a `Why` line stating what it is really asking, so a
  reader can judge whether the framing is fair rather than inferring it from the
  score.
- Five controls, including a full-dump baseline that passes 46.9%, bound how much
  credit the metric gives for judgement over volume.
- §6 exists.

**What these numbers license.** That on 32 scenarios written by us, the
configurations we assembled separate on leakage and signal rather than carry, and
that the separation is not sampling noise *within this suite*. **What they do not
license:** a ranking of these products, a claim about the field's capabilities in
general, or a claim that the difference would survive scenarios we did not write.
The whole of CONTINUITY-32 is an **author set** and should be cited as one.

### 7.1 What would license the stronger claims

Stated as a plan rather than a result, because none of it has been run:

1. **Freeze and publish** the current 32 scenarios and brain's scores on them.
   Everything below is scored against a frozen system.
2. **A held-out set**, authored after that freeze, by the same authors but
   without access to per-scenario results.
3. **An external set**, authored by independent users of these systems, with
   required/forbidden/signal labels assigned by two blinded annotators and
   inter-rater agreement reported. The held-out and external sets, not this one,
   would be the primary evidence.
4. **Authentic configurations**: `mem0` with `infer=True`, Letta with its agent
   loop running, each product as its vendor intends, reported separately from the
   retrieval-substrate comparison so both claims stay distinguishable.
5. **Ablations of brain**, to attribute the gap to a mechanism rather than a
   product: without supersession-at-recall; without the signal layer; without
   durable markdown as source of truth; without two-tier budget prioritisation;
   retrieval-only.
6. **Stronger baselines**, because full-dump is a volume baseline and not an
   intelligent one: an LLM summariser over the full history, a hand-authored
   structured project log, a temporal knowledge graph, and retrieval with
   reranking plus a rule-based supersession and provenance layer. If a plain LLM
   summariser closes most of the signal gap, the interesting claim of this paper
   is much smaller than it currently reads.
7. **A downstream utility study**, which is the one that actually tests the
   thesis. Give a successor agent — and separately a human developer — one of:
   raw retrieval output, full-dump, brain's briefing, or a human-written handoff,
   then measure time to complete the follow-on task, repeated failed attempts,
   actions taken on superseded values, tokens consumed, and success rate. Until
   this exists, the paper demonstrates rubric performance and merely argues for
   handoff utility.
8. **Sensitivity analysis**: pass rates as the 0.75 bar sweeps, and as the token
   budget tightens.

### 7.2 Other threats

**Competing systems ran with extraction disabled** — `mem0` with `infer=False`,
Letta with its agent loop off. This is favourable to them on retrieval, since
nothing is lost to a small model's extraction, and unfavourable on
reconciliation, which is exactly where the weakness we report (supersession)
sits. **This is the caveat most likely to change conclusions**, and it means the
comparison as run is between one full pipeline and several retrieval substrates.
Running either product authentically requires one or more LLM calls per event,
and a single scenario writes upward of two hundred events.

**32 scenarios.** One case moves the headline by ~3.1 points, and the Wilson
intervals in §5 span 20–30 points. Per-scenario results are emitted by the
harness; the paired discordance tables are not currently published and should be.

**Budgets are generous relative to scenario size**, which is why full-dump still
reaches 46.9%. A tighter budget would separate the field further and would be a
different benchmark.

**The metric is a hypothesis, not a ground truth.** The conjunctive form and the
0.75 bar encode a belief about what a successor needs. §7.1(7) is what would test
that belief; until then, a system that scores well here is a system that matches
our model of a good handoff.

**Single machine, single embedding model, single run.** No variance across
hardware, seeds, or embedding choice is reported. Systems also differ in
operational weight in ways the numbers do not capture: Letta alone requires
PostgreSQL with pgvector and a running server.

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

Agent memory is evaluated by answer correctness and deployed for handoff. The
component capabilities a handoff needs are not unknown to prior benchmarks —
updates, temporal reasoning and abstention are named abilities in LongMemEval —
but they are scored one at a time, on whether an answer is right, with no cost
for what else the response carries and no credit for saying what was already
ruled out.

On a 32-scenario suite built to score them together, the systems we configured
agree closely on what they can find (82.8–89.1% carry) and differ by 37 points on
what they hand over, with leakage and framing accounting for the difference. That
is a finding about this suite, whose scenarios we wrote and whose bar we chose;
§7.1 lists what would be needed to make it a finding about the field, and we
would rather someone else ran it.

We do not claim the system that scores best here is the best memory system. We
claim that a memory system should be judged by whether it lets the next agent act
correctly — not merely by whether it can retrieve a relevant sentence — and offer
a falsifiable instrument for arguing about that.

---

## Appendix A — Where the difference came from

Eight skills scored 0% across the entire field in the first run of this suite.
Seven were subsequently held by one system with no other system moving. Each
change was behavioural rather than suite-fitting, and each shows up on scenarios
other than the one that exposed it. This appendix is also the clearest statement
of the threat in §7: these are seven optimisations against a benchmark the
optimiser wrote.

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
4. **A portable interchange format.** Durability is currently measured as "does
   it survive cache deletion"; the stronger property is whether a memory can be
   moved between systems at all.
5. **Everything in §7.1**, which matters more than items 1–4 and is listed
   separately because it is about the evaluation rather than the system.
