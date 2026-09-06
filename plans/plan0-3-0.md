Continuity for coding agents — 0.3.0: the subtraction release, plus
`brain update`

     This file originally planned a different, smaller 0.3.0 (Stage 3 privacy
     controls, MCP resources, abandoned-session handling, continued
     bughunting). Partway through, a bigger question overtook it: whether the
     ambient-capture half of this product — timeline, routines, secretary
     briefs, weekly reviews, the rollup review queue, the presence orb, voice
     — belonged in the same repository as continuity for coding agents at
     all. It didn't. This file records what actually shipped instead. The
     original Stage 3 / MCP-resources / abandoned-sessions work was not done
     here; it is carried forward at the bottom, re-scoped for a codebase that
     no longer has a capture layer to build privacy controls for.

     ---

     What shipped

     One sentence now describes the whole product: Logos ferries context
     between agents and sessions. Everything that was not that got deleted,
     not paused behind a flag:

     - `internal/capture` (incl. `sources/`), `internal/event`,
       `internal/rollup`, `internal/routine`, `internal/presence`,
       `internal/voice` — gone. Roughly 9,000 of 48,600 Go lines. Four of
       five capture sources were macOS-only; none of it was benchmarked or
       dogfooded (`brain doctor` on the maintainer's own vault had reported
       `capture: off` for a long time).
     - `internal/secretary` split in two: `commitment.go` (open loops —
       `Add`/`Open_`/`SetStatus`/`ResolvedSince`) stayed, wired into
       `contextpack`, `reflection`, `replay`, `context`, `session` — this is
       real continuity surface. `brief.go`/`weekly.go` (the ambient half) are
       gone, along with the `weekly` CLI command.
     - `internal/dream` narrowed to what it already three-quarters was: the
       memory-quality engine. `rem.go` never touched events and is
       untouched; `nrem.go` lost the events parameter, the gist-extraction
       step (`storeGists`, `routine.FindPeriodic`/`FindSequences`), and
       `countArtifacts`. Note for later: `storeGists` was the only
       many-observations-into-one-denser-fact mechanism in the codebase, and
       it died with its data source — real multi-memory compression is a
       0.4.0 problem, not reachable by resurrecting this.
     - CLI surface lost `capture`, `timeline`, `rollup`, `routines`, `brief`,
       `weekly`, `jot`, `presence`, `voice`, `say`, `listen`, `prune`, `name`
       (the last was voice/presence-only naming; the desktop app's own
       "what do you call the assistant" identity flow in `app/identity.go`
       is unrelated and untouched). `review` stayed — it accepts/rejects
       quarantined memories, which is not rollup-specific.
     - The desktop app survives as a vault browser: `chat.go`, `context.go`,
       `graph.go`, `identity.go`, `memory.go`, `sessions.go` kept.
       `presence.go`/`voiceinput.go` deleted; `app.go` lost `Brief()`,
       `Timeline()`, `Proposals()`/`Accept()`/`Reject()`, `Routines()`, and
       the `Recording`/`Events`/`Pending` fields that only ever described the
       deleted tier.
     - MCP surface unchanged at 15 tools — nothing there depended on rollup,
       only two comments referencing its human-approval pattern as a
       precedent, reworded in place.
     - `internal/index` now drops the five now-dead ambient-capture tables
       (`events`, `source_state`, `proposals`, `presence_state`,
       `presence_spoken`) on the first `brain index` against an old vault,
       and says exactly how many rows it discarded — silent on every open
       after, since sqlite_master has nothing left to find. Accepted
       rollup-proposal notes already written to the vault as markdown are
       untouched; they were always indistinguishable from hand-written notes
       and survive reindex, which is the actual vault-is-truth test here.
     - Docs (`README.md`, `SETUP.md`) and `.gitignore` caught up: README
       gained a first-class "nothing is observed" paragraph naming
       `brain update`'s own release check as the sole exception to "nothing
       leaves the machine"; SETUP's command table and "how it works" section
       dropped the two-tier episodic/semantic description entirely.
       `personal/DESIGN.md` and `personal/docs-design/{capture,presence,
       rollup,routines}.md` (gitignored, never committed) got
       SUPERSEDED banners rather than deletion, so the design reasoning for
       what was cut is not silently lost to a future agent tempted to
       rebuild it. `http-transport.md` was deliberately left alone — it is
       what 0.4.0's remote-MCP transport work needs.
     - `internal/selfupdate` + `brain update` / `brain update --check`:
       resolves the latest release from `github.com/Coder8124/logos`'s
       releases API, refuses outright on an unstamped dev build and under an
       npx install (nothing durable there to replace), verifies a
       downloaded archive's checksum against `SHA256SUMS`, extracts the
       binary into the target's own directory, **runs it and confirms it
       reports the expected version before replacing anything**, then swaps
       via `os.Rename` (same-filesystem, same-directory). Every failure
       names the step it happened at (`check`/`download`/`checksum`/
       `verify`/`replace`) rather than a bare "update failed". This is the
       one place in the codebase that makes a network call on its own
       initiative, scoped to only ever run when a person types the command:
       no schedule, no `doctor` integration, no MCP-startup check. The
       request itself carries no vault data and no machine identifier — a
       bare GET with `User-Agent: brain/<version>`, asserted directly by
       `TestUpdateSendsNoVaultDataAndNoQueryString`.

     ---

     What this clears the way for (0.4.0 — not started)

     0.4.0's direction is reach: ferrying context to the web chat surfaces
     too (Claude.ai, ChatGPT, Perplexity, Gemini), not only MCP-speaking
     coding agents. `brain mcp serve` is stdio-only today; Claude.ai and
     ChatGPT connectors speak remote MCP over HTTP+SSE, so the transport
     work has to come first — `personal/docs-design/http-transport.md`
     already designs this and was kept for exactly that reason. Whatever
     ships there must not rebuild what this release deleted: a browser
     extension that reads Claude.ai/ChatGPT pages is observation, same
     shape as the browser-history capture just cut, and the difference has
     to be architectural (scoped to those sites, only the active
     conversation, only on explicit invocation, never a background daemon),
     not a promise layered on top of a daemon that watches.

     ---

     Carried forward, re-scoped for the smaller codebase

     These are the original plan0-3-0 items. None were built this round —
     the cut and `brain update` were. They still apply, minus anything that
     assumed the now-deleted capture layer.

     1. Abandoned sessions still has no resolution path. `brain doctor` has
        reported real unclosed sessions (tutorbot ×3, portfoliowebsite) for
        days; `internal/session/abandon.go`'s detection is correct and
        tested. What's still open is the other side — there is no way to
        get from "doctor says 4 abandoned" to resolving one short of writing
        a checkpoint by hand. Candidates, undecided:
        - `brain sessions <project> --close <id>` — a minimal
          machine-written checkpoint, explicitly marked as such so it is
          never confused with a real handoff.
        - `brain doctor --fix`, interactively, for every FAILED session at
          once.
        - Nothing — this is a standing signal, not a queue to empty, and
          doctor's job is done the moment it tells the truth.
        `before_you_try` this before building any of it — a session-closing
        tool has a real failure mode (closing one that is slow, not dead).

     2. MCP resources. Still no `resources.go` in `internal/mcpserver`;
        every surface there is a tool. The cut argues for this more, not
        less, now: `list_memories`, `list_projects`, `memory_diff`,
        `pin_memory`, `exclude_memory`, `forget` cost every session tokens
        on the tool surface regardless of whether that session ever curates
        memory. Moving the read-only, cacheable ones to resources (or CLI)
        is 0.4.0 unless it turns out to be small enough to fold in first.

     3. Continued bughunting in `internal/replay`, `internal/graph`, and the
        `memory_diff`/`reflect` report commands — `internal/routine` and
        `weekly` drop off this list; both are deleted. Same method as every
        prior pass: failing test first, prove it fails on current code, fix,
        prove it passes. Do not re-open anything already ruled out (the
        nil-router sweep, `project.go`'s load*-swallows-errors class,
        consent/quarantine locking, `abandon.go`'s SQL) without a specific
        new lead.

     Stage 3 (`CaptureStatus`/`StartCapture`/`PauseCapture`/`SetPolicy`/
     `DeleteRange`/`ExportVault`) is dropped outright, not carried — it was
     privacy controls for the capture layer this release deleted. If a
     comparable need resurfaces for what remains (memory, sessions,
     checkpoints), it gets scoped fresh against that surface, not resurrected
     from a design built for ambient capture.

     Out of scope, unchanged: Homebrew, Nix, `curl | sh`, macOS notarization,
     a permissions engine, multi-agent leases, any expansion of an ambient
     capture/secretary/voice/presence vertical.

     ---

     Verification

     - `go build ./... && go vet ./... && gofmt -l . && go test ./...` and
       `go test -count=1 -tags chaos ./chaos/...` clean after every commit —
       held for all eleven steps of this release.
     - `grep -rn "internal/\(capture\|event\|rollup\|routine\|presence\|
       voice\)" --include="*.go" .` returns nothing.
     - A vault with old capture data: `rm -rf .brain && brain index` — the
       previously accepted rollup notes are still searchable, and the
       reindex printed how many dead rows it dropped (both confirmed with a
       hand-built scratch vault and a standalone row-injector).
       `BRAIN_VAULT=/tmp/scratch-vault brain doctor` — no `capture` check,
       not failing.
     - MCP over stdio still handshakes and lists 15 tools.
     - `internal/selfupdate`'s five plan-named tests plus a happy-path swap
       test and a no-new-release no-op test, all passing against an
       `httptest` server — never a real GitHub call or a real exec.
     - Not yet done as part of this plan: `brain bench continuity` regression
       check (confirm the cut didn't move the recall number), `cd app &&
       wails build`, and the manual end-to-end `brain update` run against
       the real GitHub release once a tagged build exists to test it with.
