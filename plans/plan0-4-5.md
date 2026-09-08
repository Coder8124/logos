# 0.4.2 bugfixes + 0.5.0 web-AI bridge

## Context

Driving the CLI end-to-end on a scratch vault surfaced four real bugs (not
usage mistakes) against CLAUDE.md's own invariants — a silent no-op that
violates "a failure is reported, never swallowed", a retrieval command that's
blind to a whole data source, undifferentiated CLI errors, and a "project"
naming collision between two independent subsystems. Fix all four first,
since they undercut trust in exactly the receipts-and-honesty story this
product sells on.

Separately, for 0.5.0: connect brain to AI chat run in a browser (ChatGPT,
Claude.ai, Perplexity), not just IDE/desktop MCP hosts. Confirmed by research
before committing to an approach — see "Web bridge research findings" below —
that this has to be a local-only browser extension, not Claude.ai's official
remote-connector flow, to keep CLAUDE.md's "nothing leaves the machine"
invariant intact. Decision made with the user: build the local-only extension
for all three sites, no server-side tunnel exception.

---

## Part A — four bug fixes

### A1. `memory forget`/`memory pin` silently succeed on a nonexistent id

`internal/memory/memory.go`: `Forget` → `forgetRow` (~line 794-821). The kind
lookup ignores `sql.ErrNoRows`, and `DELETE ... WHERE id = ?` never errors on
zero rows affected — so forgetting id 999 returns `nil`, the CLI prints
`forgotten.`, and `logEventIn` writes a fake `forgotten #999` line into
`memory_log`.

Fix: `forgetRow` checks `RowsAffected()` after the `DELETE`; if it's 0, return
a `not found` error before calling `logEventIn`, and `Forget` propagates it.
Same shape check for the `kind` lookup (`QueryRow(...).Scan` — check
`sql.ErrNoRows` explicitly rather than swallowing every scan error). Update
`cmd/brain/memory.go`'s `forget` case error message if needed so it reads
`no memory #999` rather than a raw SQL-shaped error.

Test: `TestForgetANonexistentIDReturnsAnError` in `internal/memory/memory_test.go`
(a `TestForget` already exists there — model the failure case after it), plus
one in `cmd/brain` exercising `brain memory forget 999` → non-zero exit,
`error: no memory #999` on stderr, and no new `memory_log` row.

### A2. `brain search` / `brain ask` never see memories

`internal/index/search.go` (`Search`, `Ask`) and `hybrid.go` (`LexicalSearch`,
`HybridSearch`) only touch `notes`/`notes_fts`/`embeddings` — the vault's
markdown side. `internal/memory` keeps its own `memories` table, its own
embeddings, and its own hybrid ranking (`internal/memory/hybrid.go`,
`memory.Recall`), wired only into the MCP `recall` tool and `brain memory`
subcommands. Reproduced: `memory add` → `index` (confirmed embedded) →
`search`/`ask` on matching text both come back empty.

Reuse rather than rebuild: `memory.Recall(db, p, embedModel, query, k)`
already exists, already applies the pin/quarantine/exclude rules (see
`internal/memory/pin_test.go`), and already degrades to `All()` when `p` is
nil — the same shape `HybridSearch` degrades to lexical.

Fix, in `internal/index/hybrid.go`'s `HybridSearch` (the one choke point
`search` and `ask` both already funnel through, per its own comment about
`ask` degrading the same way `search` does):

- After computing the fused note hits, call
  `memory.Recall(ix.DB, p, model, query, k)` — pass `nil` for `p` when
  `model == ""`, mirroring the existing `p == nil || model == ""` guard, so
  `BRAIN_EMBED=off` degrades memories to `All()` the same way notes degrade
  to lexical.
- Convert each returned `Memory` into a `Hit` (e.g. `Slug: fmt.Sprintf("memory#%d", m.ID)`,
  `Kind: "memory"`, `Title: string(m.Kind)`, `Body: m.Text`), scored into the
  same RRF fusion as the lexical/vector note arms (or appended and re-sorted
  by a comparable synthetic score — keep it simple: interleave by rank, since
  memory scores and note RRF scores are not on a shared scale).
- `LexicalSearch` (the no-embedder path) needs the equivalent: call
  `memory.Recall(ix.DB, nil, "", query, k)` (nil provider forces the `All()`
  fallback, already substring/salience-ranked) and merge those in too, so a
  machine with no runtime still surfaces memories via `search`/`ask`.
- `Ask`'s context-assembly loop (`ix.Ask` in `search.go`) already iterates
  `hits` generically by `Title`/`Slug`/`Body`, so no change needed there once
  `HybridSearch` returns memory-flavored hits.

Test: `TestHybridSearchSurfacesAStoredMemory` and
`TestLexicalSearchSurfacesAStoredMemoryWithNoRuntime` in
`internal/index/hybrid_test.go`, following the pattern of the existing
`TestHybridSearchWithoutAnEmbedderFallsBackToLexical`. Manually re-verify the
exact repro from this session (`memory add` → `index` → `search`/`ask`) on a
scratch vault.

### A3. Every CLI usage error prints the same undifferentiated help dump

`cmd/brain/main.go`'s dispatch `switch`: `search`/`ask`/`context` are guarded
by `cmd == "search" && rest != ""` etc., so a bare `brain search` falls
through to the same `default: usage()` as a genuinely unknown command
(`brain remember`, `brain boguscommand`). `usage()` prints the full help
block to stderr and exits 2 either way — nothing says which mistake was
made. Contrast with `brain memory <bad-subcommand>`, which already does this
right (`error: usage: brain memory [add|forget|pin|...]`).

Fix:
- Add explicit empty-arg branches for `search`, `ask`, `context` (and any
  other `rest != ""`-gated case) that return a targeted usage error, e.g.
  `case cmd == "search": return fmt.Errorf("usage: brain search <query>")`
  before falling through — matching the one-line style every other command's
  `runX` already uses.
- Change the `default` case so an unrecognized command name prints
  `error: unknown command %q` to stderr before the help text (or instead of
  it — a short "unknown command, run `brain help`" is friendlier than
  reprinting the whole block every time), rather than making a typo
  indistinguishable from `--help`.

Test: `cmd/brain` table test asserting `brain search` (no query) and
`brain boguscommand` produce different stderr messages, both exit 2 (or 1,
matching whichever the new explicit branches return — keep usage() page's
own exit(2) for the true-unknown-command case, use the normal exit(1) error
path for the other `runX`-style commands so behavior stays consistent with
every other command's usage error).

### A4. `brain projects` / `brain project <name>` silently disagree with
`sessions`/`continuity`/`reflect` about whether a project exists

`cmd/brain/projects.go`: `projectsCmd`/`projectCmd` read from
`project.Detect`/`project.Get`, backed by `openEvents()` — the
activity-rollup dossier, a genuinely different subsystem from the
vault/checkpoint/memory data that `sessions`, `continuity`, and `reflect`
read via `openIndex()`. This is a legitimate, deliberate separation (rollup
dossiers vs. raw continuity records) — not a bug to merge — but the shared
word "project" plus the phrasing `no project matching "demo"` reads as "this
project doesn't exist," which is false for a project with real checkpoints
and memories.

Fix (message-only, not a data-model change): in `projectCmd`, when
`project.Get` returns not-ok, do a cheap existence check against the vault
(e.g. `session.Sessions(vault, name)` or an existing helper that lists
checkpoints for a project) before erroring. If checkpoints/memories exist for
that name, say so specifically:
`no activity dossier for "demo" yet (it has checkpoints/memory, but the
rollup that builds dossiers from activity hasn't run) — try
'brain sessions demo' or 'brain memory log' meanwhile`.
Otherwise keep the current `no project matching %q` message. Same idea for
`projectsCmd`'s "no projects detected yet" line — mention when there are
projects with checkpoints/memory even though none have an activity dossier
yet.

Test: `TestProjectCommandDistinguishesNoDossierFromNoProject` in
`cmd/brain` — seed a checkpoint for a project with no rollup activity, assert
`brain project <name>` names the actual situation instead of implying the
project is unknown.

---

## Part B — 0.5.0: local web-AI bridge (ChatGPT, Claude.ai, Perplexity)

### Web bridge research findings (why this shape)

- **Claude.ai's official "Add custom connector"** calls a remote MCP server
  from Anthropic's own cloud infrastructure, not from the user's browser —
  confirmed via Anthropic's connector docs. It requires a public HTTPS URL
  reachable from Anthropic's IPs; localhost/private-network servers are
  explicitly unsupported. Using this path for brain means exposing the vault
  to the internet (a self-run tunnel), which breaks CLAUDE.md invariant #5.
  **Not used.**
- **Chrome's native WebMCP API** (a page can *expose* itself as an MCP
  server to an extension) landed in Chrome 146 Canary (Feb 2026) and is only
  at Origin Trial in Chrome 149 — real, but immature and the wrong direction
  anyway (it's for a page exposing tools, not a page consuming a local one).
- **What already works, in production, today**: browser extensions like
  MCP SuperAssistant and the Perplexity Web MCP Extension inject a
  tool-bridge into the chat page's DOM and talk to a local bridge process
  over `localhost` only — nothing leaves the machine. This is the path we're
  building, adapted to brain's own tool set rather than adopting a
  third-party extension's codebase.
- **Real constraint this uncovers**: these consumer web UIs (chatgpt.com,
  claude.ai, perplexity.ai) don't expose a public, first-party "call this
  local tool" hook to third-party extensions. The existing tools in this
  space work by injecting an in-page affordance (a panel/button, or a
  system-prompt addendum describing available tools) and parsing the model's
  response for a tool-call-shaped block, then executing it locally and
  feeding the result back in as if the user had pasted it. That's DOM/prompt
  engineering against three UIs that can change without notice — a
  fundamentally different maintenance burden than the rest of this codebase,
  and worth being explicit about rather than promising a clean, permanent
  integration.

### B1. Local HTTP/WebSocket transport for `internal/mcpserver`

`internal/mcpserver/server.go` already separates `Server` (shared, immutable:
DB, vault, embed model) from `Session` (per-connection state) specifically
"as the precondition for any transport where the client is not the process
that started us" — this is already built for exactly this. Request dispatch
is `sess.handle(req request) *response`, transport-agnostic.

Add `internal/mcpserver/http.go`:
- `func (s *Server) ServeHTTP(addr string) error` — an `http.Server` bound to
  `127.0.0.1:<port>` only (never `0.0.0.0`), default port e.g. 8137, one
  `Session` per WebSocket connection (or per bearer-token pairing, if we go
  plain HTTP+SSE instead of WebSocket — pick WebSocket for parity with what
  MCP SuperAssistant's bridge already expects extensions to speak).
- **Local-network safety, not optional**: any webpage a user has open can
  otherwise send requests to `localhost:8137` (classic DNS-rebinding /
  drive-by-localhost attack) unless we gate it. Two controls together:
  1. A pairing token, generated once by `brain mcp serve --http` and printed
     to the terminal (`brain doctor` shows a redacted "paired: yes/no"),
     entered once into the extension's options page — the same shape Docker
     Desktop's and Ollama's local API tokens use. Reject any request missing
     it.
  2. Origin allowlist on the WebSocket upgrade: only accept
     `chrome-extension://<our-extension-id>` (and the Firefox equivalent),
     never `*` and never a bare page origin.
- New CLI surface: `brain mcp serve --http [--port N]`, alongside the
  existing stdio `brain mcp serve`. Reuses `runMCPServe`'s existing provider/
  router setup in `cmd/brain/mcp.go` — add a flag branch, not a new command.

Test: `internal/mcpserver/http_test.go` — a real client dial (like the
existing `testClient` used in the stdio integration tests) but over a
`httptest.Server`/local listener: unpaired request rejected, wrong-origin
WebSocket upgrade rejected, correct pairing token + origin gets the same
tool responses the stdio integration tests already assert.

### B2. Browser extension (new top-level `extension/` directory)

Manifest V3, three content scripts (one per site: chatgpt.com, claude.ai,
perplexity.ai) sharing one background-service-worker bridge to the local
WebSocket server from B1, plus an options page for the pairing token.

Given the DOM/prompt-injection reality above, scope 0.5.0 honestly:

1. **Ship the bridge + one site well first** (ChatGPT — the technique is
   proven there by prior art) before spreading across three. Each site's
   content script is real, separately-maintained integration work, not a
   config difference.
2. Claude.ai and Perplexity content scripts follow the same bridge, added as
   the first two follow-on content scripts once the ChatGPT one is solid —
   still inside 0.5.0 if time allows, called out as a stretch item rather
   than a committed one so the release isn't blocked on the flakiest part.
3. Each content script's job: describe brain's tool set (reuse the existing
   tool descriptions from `internal/mcpserver/tools.go` — the wording is
   already written for a model, not a human) either via that site's actual
   tool-calling surface where one exists, or by injecting a system-prompt
   addendum and parsing a tool-call block from the response otherwise; then
   call the WebSocket bridge and paste the result back into the page as the
   next turn.

Test: manual only, by nature (no headless harness for chatgpt.com's live DOM
belongs in this repo's CI) — but record the manual procedure in
`extension/README.md` and note it as "manual, and recorded as manual" in the
checkpoint per this project's own convention.

---

## Verification (Part A)

```sh
go build ./... && go test ./... && go vet ./... && gofmt -l .
go test -tags chaos ./chaos/...
```

Then on a scratch vault (`BRAIN_VAULT=/tmp/scratch-vault-try`):
- `memory add` a fact, `index`, `search`/`ask` for it — confirm it now
  surfaces (A2).
- `memory forget 999999` (nonexistent) — confirm non-zero exit and an error,
  and `memory log` shows no new `forgotten` line for it (A1).
- `brain remember foo` / `brain search` (no query) — confirm distinct error
  messages, neither the full help dump (A3).
- `checkpoint`/`memory add` for a project with no rollup activity, then
  `brain project <name>` — confirm it doesn't claim the project is unknown
  (A4).

## Verification (Part B)

- `brain mcp serve --http --port 8137` starts, `brain doctor` reflects it.
- A short Python/Node script dials the WebSocket with the right token +
  origin header and gets a real tool response; wrong token and wrong origin
  are both rejected.
- Manual: load the unpacked extension, pair it, open chatgpt.com, ask it to
  recall something stored in the vault, confirm the round trip.
