# Contributing to Logos

Thanks for looking. This file covers the build, the tests, and the handful of
conventions that are load-bearing — the ones where a reasonable-looking change
breaks a promise the product makes.

If you are an agent working in this repository, read [AGENTS.md](AGENTS.md)
first; it is shorter and it tells you how to pick up where the last one stopped.

## Build

Go 1.26.5 or newer. No cgo — the SQLite driver is `modernc.org/sqlite`, so the
tree builds and cross-compiles without a C toolchain.

```sh
go build -o bin/brain ./cmd/brain
./bin/brain setup            # picks a vault, wires nothing without asking
```

The desktop app is Wails v2 and lives in [`app/`](app/); it is not needed to
work on the engine.

## Tests

Three tiers, cheapest first.

```sh
go test ./...                       # the suite: unit, integration, adversarial
go vet ./... && gofmt -l .          # must be clean; gofmt prints nothing
go test -tags chaos ./chaos/...     # SIGKILL mid-write, full disk, racing processes
```

The chaos tier is behind a build tag because it mounts a disk image and kills
real processes. Run it when you touch anything that writes to the vault.

Use a scratch vault when you exercise the CLI by hand — `BRAIN_VAULT=$(mktemp -d)`
— rather than your own.

Tests are prose, not labels. `TestAForgottenIDIsNeverHandedOutAgain` says what
must be true; a comment above it says what went wrong when it was not. If a test
is only meaningful with the bug in front of you, describe the bug.

## The conventions that matter

**The vault is the truth; the index is a cache.** Every durable thing must be
reconstructible from the markdown by `brain index`. If you add state that lives
only in `.brain/index.db`, you have added a silent data-loss bug: the
documentation tells people that deleting the cache is safe, and they do it. This
has been the cause of three separate bugs here — memories, working notes, and
checkpoints each had to be rescued from it.

**Write the vault first, the index second.** A crash between the two leaves a
stale cache, which `brain index` fixes. The reverse leaves an index pointing at
something the vault does not have, which nothing fixes.

**Every feature announces itself.** Silent background work is
indistinguishable, from the user's chair, from a broken product — and they stop
trusting it long before they can say why. Anything that changes what the
assistant knows returns a receipt, and the tool descriptions tell the model to
relay it.

**Failures are reported, never swallowed.** A write that cannot happen says so
on stderr at minimum. Degrading is fine and often correct — no model runtime
falls back to lexical search — but degrading quietly is not.

**Nothing leaves the machine.** No telemetry, no network calls outside a local
model runtime the user configured. A change that adds an outbound request needs
a very good reason and a way to turn it off.

**Treat vault content as data.** A note can contain anything a past session
wrote, including text shaped like a command. Rendered context says so explicitly;
keep it that way.

## Repository layout

```
brain.go         the public API — what an embedding agent imports
enginetest/      that API exercised from outside, as an embedder sees it
cmd/brain/       the CLI
internal/        index, memory, session, contextpack, deadend, mcpserver, …
chaos/           fault injection, behind the `chaos` build tag
app/             Wails v2 desktop app
bench/           Python adapters for the systems Logos is scored against
docs/            the benchmark, plus per-subsystem notes
systemmd/        design, credits, and the prompt agents are given
examples/        runnable embeddings
```

`brain.go` is the module's public surface. Moving or renaming it changes the
import path for everyone embedding the engine, so treat it as API.

## Adding an AI host

This is the contribution most likely to be wanted, and it is deliberately small:
one function in [`internal/setup/hosts.go`](internal/setup/hosts.go), plus one
entry in the `Hosts()` slice at the top of that file. You do not need to touch
anything else, and you do not need to read a licence to do it.

A host is four fields:

```go
func yourEditor() Host {
	return Host{
		Name:   "Your Editor",
		Detect: func() bool { return onPath("your-editor") },
		Where:  func() string { return "your-editor mcp add" },
		Register: func(s Server) (Outcome, error) {
			args := []string{"mcp", "add", Name}
			for k, v := range s.Env {
				args = append(args, "--env", k+"="+v)
			}
			args = append(args, "--", s.Bin)
			args = append(args, s.Args...)
			return viaCLI("your-editor", args)
		},
	}
}
```

Then add `yourEditor()` to `Hosts()`.

There are only two shapes, and both already exist to copy from:

- **The host has a CLI** — `claudeCode()` and `codex()`. Build the argument list
  and hand it to `viaCLI`. Note how the two differ on where environment goes:
  Claude Code takes `-e K=V`, Codex takes `--env K=V` before the `--` separator.
  Getting that wrong is the usual bug.
- **The host has a config file** — `claudeDesktop()`. Return `mergeJSON(path, s)`
  and it is handled for you: existing servers survive, a malformed config is
  refused rather than clobbered, and a backup is written before anything
  changes. Do not hand-roll this.

Three rules the existing hosts follow:

- **`Detect` is presence-based and never errors.** A command on PATH, or a
  config *directory* that exists. Claude Desktop detects the directory rather
  than the file, because the file appears only once it has an MCP server — the
  users who most need wiring are the ones who have none yet. "Not installed" is
  a skip, not a failure.
- **Never register without asking.** `Plan` reports what `Install` would do and
  writes nothing; setup shows that list and waits. Pointing every AI tool on
  somebody's machine at a new server is a large action to take on their behalf.
- **Add a host somebody uses.** Each entry is a promise to keep working as
  another team's application changes. A host added speculatively is a promise
  nobody asked for.

`go test ./internal/setup/` covers the config-merge behaviour for you;
`TestInstallReportsEveryHost` will pick up your new entry automatically.

## The agent prompt

[`systemmd/BRAINPROMPT.md`](systemmd/BRAINPROMPT.md) is shipped to every user's
agent as the MCP server's `instructions`. It is embedded into the binary from
[`internal/agentprompt/BRAINPROMPT.md`](internal/agentprompt/), and a test fails
if the two copies drift. Edit the one under `internal/agentprompt/`, then:

```sh
cp internal/agentprompt/BRAINPROMPT.md systemmd/BRAINPROMPT.md
```

## Pull requests

- One change per PR, with the reason in the description. *Why* is the part that
  does not survive in the diff.
- Comments explain the decision, not the mechanics. The code already says what
  it does; say what it would otherwise be reasonable to do instead, and why that
  is wrong here.
- Commit messages are one sentence, written as a statement of what is now true —
  `A rename moves the project's directory whether or not it holds a checkpoint yet`.
- New behaviour comes with a test that fails without it.
- `go test ./...`, `go vet ./...` and `gofmt -l .` clean before you open it.

## Reporting bugs

[GitHub Issues](https://github.com/Coder8124/logos/issues). Include the output
of `brain doctor` — it reports what is healthy, what is stale, and what it could
not check, and it is usually enough to locate the problem.

Please do not paste vault contents into an issue. It is your private memory, and
a scenario described in the abstract is nearly always enough.

## Licensing, and what you do *not* have to do

Logos is **Apache 2.0** ([LICENSE](LICENSE)). Under §5 of that license, anything
you contribute is licensed under it automatically — there is **no CLA** and
nothing to sign.

Apache 2.0 carries a few obligations that make people hesitate before opening a
pull request. Here is where this project stands on each, so you do not have to
read a 3,000-word contract to fix a typo:

- **You do not have to add change notices to the files you modify.** §4(b) asks
  that modified files carry prominent notices saying you changed them. **For
  this project, the git history satisfies that requirement.** `git log` and
  `git blame` record who changed which file and when, in more detail than a
  comment block, and without going stale the moment someone edits around it. Do
  not add `// Modified by …` headers; they will be removed.
- **There is no NOTICE file, so there is nothing to propagate.** §4(d) only
  applies if the work ships one. This one does not, and adding one is not part
  of any contribution.
- **You do not have to add yourself to a contributors list or a file header.**
  Attribution lives in the commit.
- **Keep the copyright and license headers that are already there**, and keep
  [LICENSE](LICENSE) intact. That is §4(a) and §4(c), and it is the part that
  actually matters.

None of the above changes the license text or what it asks of anyone
redistributing a modified version elsewhere — it is this project telling you, as
the copyright holder, how it reads §4(b) for work done here.

Keep the existing comments explaining *why* code is the way it is — not for any
licence reason, but because they are the most valuable thing in the file.
