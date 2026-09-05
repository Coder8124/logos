---
name: context-connect
description: Use before relying on brain/Logos in a session — confirming the MCP server or CLI is actually reachable, finding which project you're in, and knowing the CLI fallback for every tool. Read this before continuity; continuity assumes the connection already works.
---

# Connecting to brain

brain (product name Logos) is markdown on the user's disk, read through either
an MCP server or a `brain` CLI on PATH. Before you rely on either, confirm the
connection is real — a silently broken connection is worse than an honest "I
don't have it", because nobody notices the gap until a checkpoint that should
exist doesn't.

## Every MCP tool has a CLI equivalent

If the MCP server is not wired into this host, none of this is unavailable —
it is one shell call away. Nothing here needs a network, an API key, or a
model.

| tool | command |
|---|---|
| `resume(project)` | `brain resume <project>` |
| `context(task)` | `brain context "<task>" --project <project>` |
| `before_you_try(approach)` | `brain tried "<approach>"` |
| `why(path)` | `brain why <path>` |
| `note_progress(text)` | `brain note <project> "<text>"` |
| `remember(text)` | `brain memory add "<text>"` |
| `checkpoint(…)` | `brain checkpoint <project> --task "…" --next "…" --failed "…"` |
| — | `brain doctor` — what is healthy, what is stale, what is unchecked |

## Which project am I in?

The project name comes from the working directory unless the repository names
itself in a `.logos-project` file at its root. Ask rather than guess:

```sh
brain project-name
```

A wrong name is fixable, and the fix carries the project's history with it:

```sh
brain project rename <old> <new> --dry-run
```

## If brain is not reachable

Every command degrades rather than failing outright: with no local model
runtime it falls back to lexical search, and with no vault it says so
explicitly. If `brain` is not on PATH, check `~/go/bin`, `/opt/homebrew/bin`
and `/usr/local/bin` before concluding it is absent — a GUI-launched host does
not always inherit a login shell's PATH.

Do not silently proceed without continuity. If neither the MCP tools nor the
CLI resolve, tell the user brain is unreachable here and carry on without it —
a session with no memory is worse when nobody knows it has none.

## Once connected

This skill only covers getting to brain, not what to do with it. For the
actual workflow — checking what was already tried before proposing something,
resuming prior work, writing progress as you go, and checkpointing properly
before you stop — see the `continuity` skill.
