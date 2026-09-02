# Scope: an HTTP transport for the MCP server

**Status:** proposed, not built. Written against `internal/mcpserver/server.go`
at `bcab270`.

## Why

Logos speaks MCP over newline-delimited JSON-RPC on stdio (`server.go:21`). That
reaches every host that launches a local server process, which is most desktop
tooling — Claude Code, Claude Desktop, Cursor, Codex, VS Code, Zed. It does not
reach a host that expects to *connect to* a server rather than spawn one: the
enterprise and proprietary suites that run MCP servers as network services and
speak the spec's **Streamable HTTP** transport.

The gap is not "MCP interop". Interop is inherent — anything that is an MCP
client can already talk to Logos. The gap is that one of the two transports in
the spec is missing, and it is the one remote-first hosts use.

## What is already in our favour

- `Serve(in io.Reader, w io.Writer)` (`server.go:101`) is a real transport
  boundary. Framing, dispatch, and rendering are already separated —
  `handle`, `callTool`, and `dispatch` know nothing about stdio.
- The package is explicitly an adapter (`server.go:17`). Judgement lives in
  `internal/contextpack` and `internal/session`. No tool handler needs to change.
- `scope.go:139` already reads **MCP roots** out of the `initialize` request.
  That is the remote-correct way to learn what the client has open, and it
  exists today as a fallback. Remotely it becomes the primary.
- The server already runs without a model runtime (`mcp.go:16-31`), so an HTTP
  deployment inherits the same graceful degradation.

## The four real problems, hardest first

### 1. Session state is baked into the process — the deep one

`Server` resolves the project **once**, from the directory the process was
launched in:

```go
project     string
projectOnce sync.Once   // server.go:69
```

> "Resolved once, on first use, because the working directory cannot change
> under a served process." — `server.go:65`

That reasoning is exactly right for stdio and exactly wrong for HTTP. A daemon
serving three clients has one working directory and it belongs to none of them.
Left alone, every remote client would scope to wherever the daemon started —
silently, and with writes going to the wrong project.

The fix is not to delete `projectFromCwd` (`scope.go:78`); it stays correct for
stdio. It is to move `roots`/`project` off `Server` and onto a per-connection
session, and to make roots authoritative when the transport is remote. A remote
client that advertises no roots should be **refused a default**, not quietly
given the daemon's — a wrong scope is worse than an error.

This overlaps the in-flight worktree-scoping work (plan item 0.4), which is
changing how a continuity context is keyed. **Land that first.**

### 2. One shared encoder — one client

```go
out *json.Encoder      // server.go:61
s.out = json.NewEncoder(w)  // server.go:105
```

`reply` and `replyErr` (`server.go:663`, `671`) write to that field. Two
concurrent HTTP requests would interleave frames on one encoder and corrupt
both. The reply path has to *return* a response value rather than write it, so
the stdio loop can encode to stdout and an HTTP handler can encode to its own
`ResponseWriter`.

This is a mechanical refactor, fully covered by the existing tests in
`server_test.go`, `nomodel_test.go` and `robustness_test.go` — which is why it
should go first and separately, with no HTTP in the commit at all.

### 3. Authentication, which stdio never needed

stdio's security model is process ownership: if you can spawn the server, you
are already the user, and the vault is already yours to read. A socket has no
such property, and the tool surface includes writes to markdown files on disk.

Minimum viable and not more: bind **localhost only** by default, require a
bearer token generated on first run and stored `0600`, and validate the `Origin`
header — the last is the spec's own guidance against DNS-rebinding attacks on
local MCP servers. Binding to a routable address should require an explicit flag
and print what it is exposing.

Not OAuth. Not now.

### 4. SQLite contention

`index.Open` sets `busy_timeout` because the CLI and the desktop app touching
one vault is the normal arrangement, not an edge case (`mcp.go:44-46`). N
concurrent HTTP sessions raise write contention on a single-connection SQLite
file. Probably fine at the scale this runs at; needs a load test rather than an
assumption, because the failure mode is a returned error on a memory write.

## Proposed shape

```
brain mcp serve                      # stdio, unchanged, still the default
brain mcp serve --http 127.0.0.1:7777
```

- `POST /mcp` — one JSON-RPC request, one JSON response.
- `Mcp-Session-Id` response header on `initialize`, echoed by the client
  thereafter; keys the session that carries roots and project.
- `DELETE /mcp` — ends a session.
- `GET /health` — unauthenticated, no vault data, so a deployment can be
  probed without a token.

## Phasing

| # | Step | Ships behaviour? |
|---|---|---|
| 1 | Reply path returns a value instead of writing to `s.out` | No — pure refactor |
| 2 | Session state (roots, project) moves off `Server` | No — stdio identical |
| 3 | `POST /mcp` handler + Streamable HTTP session semantics | Yes |
| 4 | Token auth, `Origin` validation, localhost-default bind | Yes |
| 5 | `brain setup` wiring + docs + `brain doctor` check | Yes |

Steps 1 and 2 are the actual work and carry all the risk. Step 3 is small once
they are done. Each step is independently mergeable and steps 1–2 are invisible
to every existing user, which is the property that makes this safe to start
before deciding whether to finish.

## Deliberately out of scope

- **The deprecated HTTP+SSE transport.** Streamable HTTP replaced it; building
  both doubles the surface to reach hosts that are being migrated anyway.
- **Server→client streaming and resumability** (`Last-Event-ID`). Every tool
  here is request/response. Nothing streams. Add it when a tool needs it.
- **OAuth / multi-user auth.** This is one person's vault on one person's
  machine. A shared-tenancy story is a different product decision, not a
  transport detail.
- **TLS termination.** Behind a reverse proxy, where it belongs.

## Open decisions

1. **Does a remote client get to write?** A read-only HTTP mode is a coherent
   and much safer default — recall and context assembly over the network,
   `remember` and `checkpoint` only over stdio. Worth considering as the
   *initial* shipping posture.
2. **Does a network-exposed vault fit the product?** "Local-first, nothing
   uploaded" is the promise on the front page. A socket does not break it — the
   data still never leaves the machine unless someone points the bind flag
   outward — but the README should say plainly what the flag does before it
   exists.
3. **Is there a real host asking?** Steps 1–2 are worth doing regardless: they
   remove a single-client assumption and untangle session state from process
   state, both of which are latent bugs today. Steps 3–5 are worth doing when a
   host that needs them is named.
