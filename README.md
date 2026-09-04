# Logos

**When one coding agent stops, the next one continues the work.**

| [Setup](SETUP.md) | [Benchmark](docs/continuity-benchmark.md) | [Agent guide](systemmd/BRAINPROMPT.md) | [Contributing](CONTRIBUTING.md) | [Site](https://coder8124.github.io/logos/) |
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
uploads nothing. `.brain/index.db` is a cache you can delete and rebuild from
the markdown. If this project dies, you keep a vault.

**Logos is built for continuity:**

- `resume` / `context` — where the last agent stopped, what it verified, what it ruled out
- `before_you_try` — whether an approach was already attempted and abandoned, here or elsewhere
- `why` — what was being decided when a given file was last worked on
- `checkpoint` / `handoff` — the durable record, with `verified` and `failed` kept deliberately separate
- Receipts on every write, so you can see the memory layer working instead of taking it on faith
- A SessionStart hook that puts the last handoff in front of the model before it does anything

**Logos is local and durable:**

- Markdown is the truth; the SQLite index is a disposable cache
- Runs with no model runtime at all — semantic search degrades to lexical, nothing breaks
- Hybrid retrieval: FTS5 plus local embeddings through Ollama, when one is available
- Vault files are written 0600, and `brain doctor` reports what the rest of the machine can read
- Survives SIGKILL mid-write, a full disk, and two processes racing on one vault ([`chaos/`](chaos/))
- Project- and worktree-scoped, so one repository's facts do not surface in another

**Logos works where you already work:**

- MCP server for Claude Code, Claude Desktop, Cursor, Codex and anything else that speaks the protocol
- Read-only tools annotated as such, so they stay available in read-only chat modes
- A CLI equivalent for every tool, for agents that only have a shell
- A Go package to embed the engine directly — `import "github.com/Coder8124/brain"`
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

Anywhere else — no Go toolchain, no clone, no build:

```sh
npx -y @noeton/logos setup
```

`setup` picks a vault, finds your local model runtime, runs the first index, and
then shows you which agents it would wire and asks before touching any of them.
`--dry-run` shows the whole plan and writes nothing.

From source, if you have Go:

```sh
git clone https://github.com/Coder8124/logos && cd logos
go build -o bin/brain ./cmd/brain && ./bin/brain setup
```

One-click buttons for Cursor and VS Code, release binaries, and wiring a host by
hand are in **[SETUP.md](SETUP.md)**.

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the build,
the test tiers, and the two conventions that are load-bearing here: the vault is
the truth, and every feature announces itself.

If you are an *agent* working in this repository, read
[AGENTS.md](AGENTS.md) first.

## On the two names

Logos is the product — the repository, the npm packages, the MCP server hosts
see. `brain` is the development name and stays one internally: the Go module
`github.com/Coder8124/brain`, the `brain` command, `BRAIN_VAULT`, and `.brain/`.
The npm wrapper installs `logos` and `brain` as the same command, so either
spelling works wherever you meet it.

## Contact

- Bugs and feature requests — [GitHub Issues](https://github.com/Coder8124/logos/issues)
- Prior work that shaped the ideas — [systemmd/CREDITS.md](systemmd/CREDITS.md)

## License

Apache 2.0. See [LICENSE](LICENSE).

**Contributors: git history satisfies §4(b).** You do not need to add
change notices to files you modify, and there is no NOTICE file and no CLA.
See [CONTRIBUTING.md](CONTRIBUTING.md#licensing-and-what-you-do-not-have-to-do).
