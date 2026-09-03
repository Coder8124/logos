# Credits & inspiration

`brain` is its own codebase, but several features were inspired by prior open
work. All of the projects below are MIT-licensed; the *ideas* were reimplemented
in Go rather than the code being copied, and the thinking is gratefully
acknowledged.

This list is kept current rather than cumulative — an entry stays only while the
feature it inspired is still in the product.

## [mempalace/mempalace](https://github.com/mempalace/mempalace) — MIT

An AI memory system that organises content spatially: people and projects are
*wings*, topics are *rooms*, content lives in *drawers*, so search can be scoped
instead of flat. Also argues for **verbatim preservation** over lossy
summarisation.

**What it inspired here:** scoped retrieval — searching within a folder/kind
(`people/`, `projects/`, `topics/`) rather than the whole vault — and keeping a
verbatim capture path (braindump) alongside the distilled notes.

MemPalace is also one of the systems brain is measured against in
[docs/continuity-benchmark.md](docs/continuity-benchmark.md), where it is the
strongest of the alternatives and beats brain outright on temporal ordering.
Being scored against the work that shaped you is the honest version of a credit.

## [huytieu/COG-second-brain](https://github.com/huytieu/COG-second-brain) — MIT

"Cognition + Obsidian + Git." Markdown-first, no database lock-in, with braindump
auto-classification, weekly pattern analysis and monthly consolidation, and a
verification-centric stance (sources required, confidence stamped).

**What it inspired here:** the **braindump** quick-capture command
(`brain jot`), which classifies a scrap of text and routes it into the review
queue. The vault-as-truth / DB-as-cache stance and confidence-stamped edges are
kindred ideas arrived at independently.

## [henrydaum/second-brain](https://github.com/henrydaum/second-brain) — MIT

A local-first microkernel runtime that indexes local files and does **hybrid,
lexical, and semantic search** with citations, plus scheduled recurring tasks.

**What it inspired here:** upgrading retrieval from pure cosine similarity to
**hybrid search** — SQLite FTS5 lexical matching fused with vector similarity by
reciprocal rank fusion — so exact terms (names, error codes, IDs) are found as
reliably as concepts. Measured at +8.9 points over vector-only on LongMemEval-S.

---

## Removed

**TurboLearn AI** previously appeared here for spaced-repetition flashcards in
the tutor build. The vertical personas were removed — brain is memory that
agents query, and a tutor was not that — so the feature and the credit both go
with them.
