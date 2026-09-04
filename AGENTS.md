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

## Checkpoint before your context runs out, not after

Do not save the handoff for the end of the session. Sessions here do not end
tidily — they end when the context window fills, and the summary you were going
to write is the one that never gets written. This has already happened on this
repository more than once, and each time the next agent paid for it.

So: **checkpoint when you finish a piece of work, and again whenever the
conversation is getting long or the user says you are losing context.** A
checkpoint is cheap and idempotent; the second one supersedes the first.

```sh
brain checkpoint brain --task "…" --next "…" --failed "…" --verified "…"
brain index          # the checkpoint is a file first; this makes it searchable
```

Two habits that make the difference between a checkpoint and a useful one:

- **`failed` is the field that pays for the whole system.** An empty `failed` on
  a session that ruled something out is the single most expensive omission
  available here, because the next agent will spend hours re-deriving it. Write
  down the thing you suspected and disproved, *including* why it looked right.
- **`verified` is what you actually ran**, each with the command that showed it.
  Anything you merely believe goes in `state`. An agent that trusts a `verified`
  line and finds it was a guess stops trusting the vault, and then the vault is
  worth nothing.

When the user asks you to "summarise into brain", "hand off", or "preserve
context", that is this — a checkpoint, not a message in the chat. The chat is
gone when the session is.

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
