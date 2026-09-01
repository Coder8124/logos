<h1 align="center">Logos</h1>

<p align="center">
  <strong>The memory you own.</strong><br>
  Continuity for AI coding agents — checkpoint in one tool, resume in another.
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/@brainyprime/logos"><img alt="npm" src="https://img.shields.io/npm/v/@brainyprime/logos?style=flat-square&color=0b7285"></a>
  <img alt="license" src="https://img.shields.io/badge/license-MIT-blue?style=flat-square">
  <img alt="platforms" src="https://img.shields.io/badge/macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-arm64%20%2B%20x64-555?style=flat-square">
  <img alt="download" src="https://img.shields.io/badge/download-~11%20MB-555?style=flat-square">
</p>

```
Claude Code  ──▶  checkpoint  ──▶  Logos  ──▶  resume  ──▶  Cursor
                                (your vault)
```

Your agent finishes a session and everything it learned goes with it — the three
approaches it ruled out, the decision it made at 2am, the reason the obvious fix
does not work here. The next agent starts from nothing and tries the first dead
end again.

Logos is the layer that survives the session. It stores what happened as
**markdown on your disk**, and serves it back over MCP to whichever agent picks
up next.

---

## Install

```bash
npx -y @brainyprime/logos setup
```

That is the whole thing. No Go toolchain, no clone, no build — this package
carries a prebuilt binary for your platform.

`setup` picks a vault (`~/brain` unless you say otherwise), runs the first index,
then **shows you which agents it would wire and asks before touching any of
them.** Decline everything and you still have a working install.

<details>
<summary><b>Wire a host by hand instead</b></summary>

No install at all — `npx` resolves the binary on demand:

```json
{
  "mcpServers": {
    "logos": {
      "command": "npx",
      "args": ["-y", "@brainyprime/logos", "mcp", "serve"]
    }
  }
}
```

That config is portable between machines, which an absolute binary path is not.
Add `"env": { "BRAIN_VAULT": "/path/to/vault" }` to point it somewhere other
than `~/brain`.

</details>

<details>
<summary><b>Claude Code users: prefer the plugin</b></summary>

```
/plugin marketplace add Coder8124/brain
/plugin install logos@logos
```

The plugin is more than the MCP server. It installs a **SessionStart hook** that
puts the last handoff in front of the model before it does anything — the
difference between continuity that works and continuity that works when the
model remembers to ask for it.

</details>

---

## What it actually does

```console
$ logos checkpoint kestrel-one --agent claude \
    --decided "aluminium frame, 6061" \
    --failed  "plastic frame — fails drop test at 1.2m" \
    --next    "quote the single-mic line"

$ logos resume kestrel-one          # from Cursor. From Codex. From anywhere.
```

The second agent gets the decisions, the dead ends, and the next step — and is
told, in the pack itself, that the dead ends are there so it does not pay for
them twice.

| | |
|---|---|
| **Negative knowledge** | Records what was *ruled out*, not just what is true. `before_you_try` answers "has this been tried?" before the agent proposes it. |
| **Structured handoff** | A checkpoint is decisions, failures, open questions and a next step — not a summary paragraph. |
| **Scope isolation** | Asking about one project does not drag in another's stale prices. |
| **Provenance** | Every fact carries where it came from, when, and how confident. |
| **Stale-plan suppression** | A next step that later work has overtaken is withdrawn, not repeated. |
| **Durability** | Delete the index, the cache, every derived artifact — the vault is markdown and nothing is lost. |

---

## It works with no AI runtime at all

Continuity — checkpoint, resume, dead ends, handoff — is markdown, SQL and
string matching. **No model on any path.** Retrieval falls back to lexical
search, which for code (identifiers, error strings, paths) is arguably the right
tool anyway.

Install a 274 MB embedding model later if you want semantic recall. Nothing
requires the 22 GB one.

| You code with | Extra download | You get |
|---|---|---|
| Claude Code / Cursor / Codex | **0 MB** | continuity + lexical search |
| …and want fuzzy recall | 274 MB | + semantic retrieval |

---

## Local-first, meant literally

- Your memory is **markdown files in a directory you chose.** Open them, grep
  them, commit them, delete them.
- `.brain/index.db` is a **cache.** Delete it and it rebuilds.
- Nothing is uploaded. There is no account, no server, no telemetry.
- If this project disappears tomorrow, **you keep a vault** that every text
  editor on earth can read.

---

## Supported platforms

| | |
|---|---|
| macOS | arm64 · x64 |
| Linux | x64 · arm64 (glibc and musl — pure Go, `CGO_ENABLED=0`) |
| Windows | x64 |

The platform packages are `optionalDependencies` gated on `os` and `cpu`, so npm
fetches only the one that matches — about **11 MB, not 55**. There is **no
`postinstall` script**: the binaries ship as real package contents, so
`--ignore-scripts` and offline installs both work.

---

## Common commands

```sh
logos setup                    # pick a vault, wire your agents (asks first)
logos doctor                   # what is working, what is not, what it cannot check
logos doctor --integration     # prove a host round-trips through to the vault
logos resume <project>         # the handoff, as a human can read it
logos tried "<approach>"       # has this already been ruled out?
logos mcp serve                # the MCP server, over stdio
```

`brain` is installed as an alias for `logos` — same command, either spelling.

---

## Two names

**Logos** is the product. **brain** is the development name, and stays one: the
repository, the Go module `github.com/Coder8124/brain`, the `brain` command,
`BRAIN_VAULT`, and `.brain/`. Both spellings work everywhere you meet them.

---

<p align="center">
  <a href="https://github.com/Coder8124/brain">Source &amp; full documentation</a> ·
  <a href="https://github.com/Coder8124/brain/blob/main/docs/continuity-benchmark.md">Benchmark</a> ·
  MIT
</p>
