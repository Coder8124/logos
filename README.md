# Logos

**When one coding agent stops, the next one continues the work.**

| [Setup](SETUP.md) | [Benchmark](docs/continuity-benchmark.md) | [Agent guide](systemmd/LOGOSPROMPT.md) | [Contributing](CONTRIBUTING.md) | [Site](https://coder8124.github.io/logos/) |
| --- | --- | --- | --- | --- |

## About

Logos is a local-first memory and continuity layer for coding agents. Claude
Code, Cursor, Codex and your own agents share one vault over MCP: an agent
checkpoints what it did, and the next one — a different model, in a different
tool, on a different day — resumes exactly where that one stopped.

```
Claude Code  ──▶  checkpoint  ──▶  Logos  ──▶  resume  ──▶  Cursor
                                (your vault)
```

It runs on your machine, against a directory of markdown files you own, and
uploads nothing. `.logos/index.db` is a cache you can delete and rebuild from
the markdown. If this project dies, you keep a vault.

**Nothing is observed.** Logos does not watch your screen, your browser
history, your files or your calendar. The only things in your vault are things
an agent explicitly wrote there — a checkpoint, a note, a memory it asked to
remember and you approved. One exception is the Claude Code plugin's
activity log, in `activity/`, which records the first 160 characters of each
prompt and of each tool call's path or shell command, with anything
secret-shaped masked — it is what a session that never checkpointed is
checkpointed from when it ends. Every repository the plugin opens is tracked
unless it says otherwise, and the first session in one tells you so: `logos
config deny`, run in a repository, keeps Logos out of it entirely (`logos config
deny --undo` lets it back in). Setup asks once about the log, `logos config
activity on` and `off` are the switch for the whole vault, an answer you gave is
never overridden, `logos watch --log` shows it, `logos index` keeps it out of the vault's
git, and a month's entries are deleted 30 days after that month ends. The other
is a raw copy of a harvested transcript, which Logos keeps only after you run
`logos import --from transcripts --keep-transcripts` — off by default, unredacted
when on, so store the vault as you would the originals; `--no-keep-transcripts`
stops it. Logos itself contacts your local model runtime and, only
when you type `logos update`, GitHub for a new release. Two things it runs reach further
on their own: once a day the session-start hook has Claude Code update the Logos plugin
(`logos plugin autoupdate off` stops it), and `logos doctor`, `logos setup` and the commands
that register logos with a host run your agent hosts' CLIs, which make their own requests to their vendors. None of them carries
anything from your vault. `logos doctor --egress` lists every address, and
[SETUP](SETUP.md#checking-that-nothing-leaves) shows how to watch for yourself. A host wired
to run Logos through `npx` is one more: npm itself asks the registry for the package on every start — the
package name, none of your data — and without a connection it waits and then
fails. An installed binary, which is what `brew install` and `npm i -g` both
give you, makes no such call.

**Logos is built for continuity:**

- `resume` / `context` — where the last agent stopped, what it verified, what it ruled out; an older ruled-out approach that matches the task comes back even from outside the time window
- `logos resume <project> <id|name>` — just one conversation, by its folder id or its intent; at a terminal, a picker
- `before_you_try` — whether an approach was already attempted and abandoned, here or elsewhere
- `why` — what was being decided when a given file was last worked on
- `checkpoint` / `handoff` — the durable record, with `verified` and `failed` kept deliberately separate; a `verified` line naming a command the activity log never saw run is marked unverified
- `logos ledger` — a project's decisions in force, dead ends and open questions in one file, rebuilt from its checkpoints and citing each one
- A decision three checkpoints recorded is proposed as project memory, and `logos review --stale` asks about each ruling and memory whose files changed since
- `ingest_harvest` / `ingest_distil` — read what another coding agent's session did and distil it yourself, into a candidate a person still has to promote
- Receipts on every write, so you can see the memory layer working instead of taking it on faith
- A SessionStart hook that puts the last handoff in front of the model before it does anything, says what re-deriving its dead ends would repeat, and says when your logos is older than the plugin

**Logos is local and durable:**

- Markdown is the truth; the SQLite index is a disposable cache
- Runs with no model runtime at all — semantic search degrades to lexical, nothing breaks
- Hybrid retrieval: FTS5 plus local embeddings through Ollama, when one is available
- Vault files are written 0600, and `logos doctor` reports what the rest of the machine can read
- Survives SIGKILL mid-write, a full disk, a read-only vault, a sync client's conflict copies and two worktrees racing on one vault ([`chaos/`](chaos/)), and its checkpoint and memory parsers are fuzzed against hand-edited files
- Project- and worktree-scoped, so one repository's facts do not surface in another

**Logos works where you already work:**

- MCP server for Claude Code, Claude Desktop, Cursor, Codex, Cline, Devin, GitHub Copilot, opencode, Amp, Grok Build and anything else that speaks the protocol
- Read-only tools annotated as such, so they stay available in read-only chat modes
- A CLI equivalent for every tool, for agents that only have a shell; `recall`, `context`, `resume` and `tried` answer `--json`
- `logos import` brings in CLAUDE.md and AGENTS.md, Cursor rules, claude-mem, Claude Code's auto memory, transcripts or git history; the instruction files and transcripts wait for your yes in `logos review`
- A Go package to embed the engine directly — `import "github.com/Coder8124/logos"`

## Does it work?

On a handoff suite built for this — 32 scenarios, nine memory systems, one
machine — Logos passes **84.4%**; the next best system passes 46.9%.

Every real system retrieves about equally well. What separates them is whether
the agent that resumes gets the *current* answer, or gets it sitting next to the
stale one it replaced. Method, per-scenario scores and the cases Logos loses are
in [the benchmark](docs/continuity-benchmark.md).

## Getting started

```sh
brew install coder8124/tap/logos-mcp && logos setup    # macOS and Linux
npm i -g @noeton/logos && logos setup                  # Windows
```

That is the whole install — no Go toolchain, no clone, no build. The formula is
`logos-mcp` from the `coder8124/tap` tap — a bare `brew install logos` installs
Logos Bible Software — and the command it installs is still `logos`. Update with
`brew upgrade logos-mcp`. Windows has no Homebrew, so there the route is
[@noeton/logos](https://www.npmjs.com/package/@noeton/logos) from npm, which
carries a prebuilt binary for your platform.

`setup` picks a vault, finds your local model runtime, runs the first index, and
then shows you which agents it would wire and asks before touching any of them.
`--dry-run` shows the whole plan and writes nothing.

For Claude Code, setup installs the Logos plugin itself when Claude Code's CLI
is on your PATH, and updates it when it is older than your logos. The plugin is
what adds the SessionStart hook that puts the last handoff in front of the model
before it does anything, so there is nothing to run inside Claude Code.

Setup names what each host gets: Claude Code restores context on its own through
the plugin, Cursor and Codex do once setup adds their session-start hook (Codex
asks you to approve it in `/hooks`), and everywhere else the tools are there to
be asked for.

From source, if you have Go:

```sh
git clone https://github.com/Coder8124/logos ~/src/logos && cd ~/src/logos
go build -o bin/logos ./cmd/logos && ./bin/logos setup
```

If you would rather not let setup touch Claude Code, install the plugin by hand
instead:

```
/plugin marketplace add Coder8124/logos
/plugin install logos@logos
```

The plugin runs the `logos` binary; it does not carry one, and Claude Code's own
installer brings neither Node nor Go. Install logos by one of the routes above
first, or the plugin has nothing to launch.

One-click buttons for Cursor and VS Code, release binaries, and wiring a host by
hand are in **[SETUP.md](SETUP.md)**.

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the build,
the test tiers, and the two conventions that are load-bearing here: the vault is
the truth, and every feature announces itself.

If you are an *agent* working in this repository, the `context-connect` and
`continuity` skills that ship with the Logos plugin (`plugin/skills/`) cover
how to connect to logos here and how to use it — this repository dogfoods its
own plugin, so both are already available in a Claude Code session.

## Formerly brain

Logos was developed as `brain`. Since 0.5.0 the old names are no longer read —
not the `brain` command, `BRAIN_*` variables, an unrecorded `~/brain` vault or a
`.brain/` directory — and logos says so on stderr, naming the fix, whenever it
finds one. A `~/brain` vault that 0.4 already recorded keeps working. `logos migrate` moves a `~/brain` vault, with its sessions and
checkpoints, to `~/logos`, leaves `~/brain` as a link to it, and re-pins the
hosts that named the old path (`--dry-run` shows the plan first). Run `logos
doctor` afterwards — [Moving a 0.4 vault](SETUP.md#moving-a-04-vault) covers
what it may still ask you to do.

## Contact

- Bugs and feature requests — [GitHub Issues](https://github.com/Coder8124/logos/issues)
- Prior work that shaped the ideas — [systemmd/CREDITS.md](systemmd/CREDITS.md)

## License

Apache 2.0. See [LICENSE](LICENSE).

**Contributors: git history satisfies §4(b).** You do not need to add
change notices to files you modify, and there is no NOTICE file and no CLA.
See [CONTRIBUTING.md](CONTRIBUTING.md#licensing-and-what-you-do-not-have-to-do).
