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
remember and you approved. The exception is the Claude Code plugin's activity
log, in `activity/`: the first 160 characters of each prompt and of each tool
call's path or shell command, with anything secret-shaped masked, which `logos
activity` shows and `logos index` keeps out of the vault's git. Nothing prunes
it yet, and removing the plugin stops it. The one network call Logos ever makes
on its own is `logos update` checking for a new release, and only when you type it; nothing
else leaves the machine, ever. Launched through `npx`, npm itself asks the
registry for the package on every start — the package name, none of your
data — and without a connection it waits and then fails. An installed binary
(`npm i -g @noeton/logos`, or a release) makes no such call.

**Logos is built for continuity:**

- `resume` / `context` — where the last agent stopped, what it verified, what it ruled out
- `before_you_try` — whether an approach was already attempted and abandoned, here or elsewhere
- `why` — what was being decided when a given file was last worked on
- `checkpoint` / `handoff` — the durable record, with `verified` and `failed` kept deliberately separate
- `ingest_harvest` / `ingest_distil` — read what another coding agent's session did and distil it yourself, into a candidate a person still has to promote
- Receipts on every write, so you can see the memory layer working instead of taking it on faith
- A SessionStart hook that puts the last handoff in front of the model before it does anything

**Logos is local and durable:**

- Markdown is the truth; the SQLite index is a disposable cache
- Runs with no model runtime at all — semantic search degrades to lexical, nothing breaks
- Hybrid retrieval: FTS5 plus local embeddings through Ollama, when one is available
- Vault files are written 0600, and `logos doctor` reports what the rest of the machine can read
- Survives SIGKILL mid-write, a full disk, and two processes racing on one vault ([`chaos/`](chaos/))
- Project- and worktree-scoped, so one repository's facts do not surface in another

**Logos works where you already work:**

- MCP server for Claude Code, Claude Desktop, Cursor, Codex, Cline, Devin, GitHub Copilot and anything else that speaks the protocol
- Read-only tools annotated as such, so they stay available in read-only chat modes
- A CLI equivalent for every tool, for agents that only have a shell
- A Go package to embed the engine directly — `import "github.com/Coder8124/logos"`
- A Wails v2 desktop app: menubar orb, panel, graph canvas

## Does it work?

On a handoff suite built for this — 32 scenarios, nine memory systems, one
machine — Logos passes **84.4%**; the next best system passes 46.9%.

Every real system retrieves about equally well. What separates them is whether
the agent that resumes gets the *current* answer, or gets it sitting next to the
stale one it replaced. Method, per-scenario scores and the cases Logos loses are
in [the benchmark](docs/continuity-benchmark.md).

## Getting started

In Claude Code, which also installs the SessionStart hook that puts the last
handoff in front of the model before it does anything:

```
/plugin marketplace add Coder8124/logos
/plugin install logos@logos
```

`logos setup` runs the same two commands for you when Claude Code's CLI is on
your PATH, and updates the plugin when it is older than your logos.

Anywhere else — no Go toolchain, no clone, no build:

```sh
brew install coder8124/tap/logos-mcp && logos setup    # macOS and Linux
npx -y @noeton/logos setup                             # anywhere with Node
```

The formula is `logos-mcp` from the `coder8124/tap` tap: a bare `brew install
logos` installs Logos Bible Software. Update with `brew upgrade logos-mcp`.

`setup` picks a vault, finds your local model runtime, runs the first index, and
then shows you which agents it would wire and asks before touching any of them.
`--dry-run` shows the whole plan and writes nothing.

Use Claude Code and another agent too, such as Cursor or Codex? Install the
plugin and run setup as well. Setup leaves the plugin's Claude Code wiring
alone.

Setup names what each host gets: Claude Code restores context on its own through
the plugin, Cursor and Codex do once setup adds their session-start hook (Codex
asks you to approve it in `/hooks`), and everywhere else the tools are there to
be asked for.

From source, if you have Go:

```sh
git clone https://github.com/Coder8124/logos ~/src/logos && cd ~/src/logos
go build -o bin/logos ./cmd/logos && ./bin/logos setup
```

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

Logos was developed as `brain`. Through 0.4.x the old names still work — the
`brain` command from npm, `BRAIN_*` variables, and an existing `~/brain` vault
or `.brain/` directory — so an existing install keeps running. Use `logos` for
anything new.

## Contact

- Bugs and feature requests — [GitHub Issues](https://github.com/Coder8124/logos/issues)
- Prior work that shaped the ideas — [systemmd/CREDITS.md](systemmd/CREDITS.md)

## License

Apache 2.0. See [LICENSE](LICENSE).

**Contributors: git history satisfies §4(b).** You do not need to add
change notices to files you modify, and there is no NOTICE file and no CLA.
See [CONTRIBUTING.md](CONTRIBUTING.md#licensing-and-what-you-do-not-have-to-do).
