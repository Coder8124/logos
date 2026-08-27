# feat/rollup-review-queue — step 3

Where raw activity becomes knowledge: episodic rows in, proposed notes out,
you approve, vault grows.

## Pipeline

```
events (SQLite)  ──T2 rollup──▶  proposal  ──review──▶  vault/*.md
                                     │
                                     └── rejected ──▶ queue.jsonl (kept)
```

Rejections are retained deliberately. "You proposed this and I said no" is the
training signal for raising the auto-accept threshold later.

## Rollup

Nightly, plus on demand. Input is the day's coalesced events grouped into
sessions. Output is a `daily/YYYY-MM-DD.md` note and zero or more **proposals**
against entity notes.

**Always re-derive from event rows, never from a previous summary.** Weekly
summaries built from daily summaries drift into mush within a month. Weekly
reads the week's events directly.

## Proposals

```jsonc
{
  "id": "01J...",
  "kind": "new_note" | "append" | "new_edge" | "merge",
  "target": "people/sameer",
  "payload": { … },
  "conf": 0.78,
  "evidence": [1421, 1422, 1509],   // event ids, always required
  "model": "qwen3.6",
  "created": 1752883200
}
```

`evidence` is mandatory. A proposal you can't trace back to specific observed
events is a hallucination with good manners, and it must be impossible to
accept one.

## Extraction

Constrained decoding via `Provider::chat(schema)` — never tool-calling. Small
local models are unreliable at tool schemas and near-perfect with a sampler-
enforced JSON schema. One narrow task per call:

| Call | Model | Job |
|---|---|---|
| classify session | T1 `gemma3:4b` | work / comms / research / idle |
| extract entities | T1 | people, projects, orgs mentioned |
| resolve entity | T2 | candidate → existing slug, or "new" |
| write daily note | T2 `qwen3.6` | prose from the session table |

Resist the urge to make this one big prompt. Small models fall apart on
multi-objective instructions, and per-task calls are individually debuggable.

## Entity resolution

The sleeper hard problem. "Sam" / "Sameer" / "@sam" must converge on one note.

1. Exact match on slug or `aliases:`.
2. Embedding similarity against existing notes of the same `type`, over θ.
3. Otherwise ask. One keystroke from the user settles it permanently by
   writing the alias into frontmatter.

Never auto-create a second note for a name that fuzzy-matches an existing one.
Duplicate-entity explosion is the failure mode that makes an auto-written vault
worthless, and it is very hard to unwind after a few weeks.

## Writing to the vault

Atomic temp-file + rename, 2s debounce on our own paths so the watcher doesn't
feed itself. Never hold a file handle open — Obsidian may have it too.

Appends target a `## Observations` section and never touch prose above it.
Anything the user hand-wrote is immutable.

## Done when

A day's activity produces a daily note plus 3–8 traceable proposals, and
accepting one writes valid frontmatter that reindexes cleanly.
