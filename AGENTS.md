# Agents working in this repository

You are not the first agent here and you will not be the last. Another agent —
possibly a different model, in a different tool, on a different day — worked on
this repository before you, and one will after you. The vault is how you talk to
them.

## Read this in order

1. **[systemmd/BRAINPROMPT.md](systemmd/BRAINPROMPT.md)** — how to use Logos:
   when to call `resume`, `before_you_try`, `note_progress` and `checkpoint`,
   what belongs in each checkpoint field, and why receipts get relayed to the
   user. It is the same text this project ships to every user's agent as the MCP
   server's instructions, which is why it lives in one place instead of being
   restated here.
2. **[CONTRIBUTING.md](CONTRIBUTING.md)** — the build, the test tiers, and the
   conventions that are load-bearing. Read it before you write code.

Everything below is the part that is specific to *this* repository and is not in
either of those.

## If all you have is a shell

Every tool has a CLI equivalent. Nothing here needs a network, an API key or a
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

The project name comes from the working directory unless the repository says
otherwise in a `.logos-project` file at its root. Ask rather than guess:

```sh
brain project-name
```

A wrong name is fixable, and the fix carries the history with it:

```sh
brain project rename <old> <new> --dry-run
```

## If Logos is not reachable

Every command degrades rather than failing: with no model runtime it falls back
to lexical search, and with no vault it says so. If `brain` is not on PATH,
check `~/go/bin`, `/opt/homebrew/bin` and `/usr/local/bin` before concluding it
is absent — a GUI-launched host does not inherit a login shell's PATH.

Do not silently proceed without continuity. Tell the user Logos is unreachable
and carry on: a session with no memory is worse when nobody knows it has none.

## Two rules that are easy to get wrong here

- **Do not put repository instructions in memory.** Logos holds *operational*
  context — what was tried, decided, and left open. Conventions and build steps
  belong in CONTRIBUTING.md, which the next agent reads anyway.
- **The vault records what was true when it was written; the code records what
  is true now.** When they disagree, the working tree wins.

---

*Looking for a copy to drop into another repository? Use
[systemmd/BRAINPROMPT.md](systemmd/BRAINPROMPT.md) — nothing in it is specific
to this project.*
