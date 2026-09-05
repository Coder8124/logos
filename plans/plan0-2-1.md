Continuity for coding agents — status and what remains (revision 2)

     Context

     Supersedes plans/plan0-2-0.md, which was audited against f0c9c25 and is
     now materially wrong: it lists several items as "not started" that have
     since landed on main, under branches that were merged and never deleted
     (feat/checkpoint-verification, feat/worktree-scope, feat/help-all,
     feat/agent-identity, feat/consent-quarantine, feat/continuity-report). It
     also names an npm scope (@ankrainc) and license (MIT) that have both since
     changed. This revision was checked directly against the code on main at
     984ca43, not against branch names or the old plan's claims.

     Everything below that plan0-2-0.md called Tier 1, Tier 2, or "not started"
     has been re-verified one item at a time. Where a check contradicts the old
     plan, the code is what was trusted.

     ---

     Status ledger

     Done and verified (unchanged from plan0-2-0.md)

     - 1.1 MCP serves with no runtime
     - 1.2 CLI degrades too
     - 1.2b Capture retention
     - 1.3 Ask before wiring
     - 1.4 Doctor says "unknown"
     - 1.5 Setup proves the vault
     - 1.6 Decision-grade model prompt
     - 2.1 Module path
     - 0.3 Git state without the model
     - 0.5 Repo → project
     - 5.9 First run is a handoff
     - 5.12 Continuity observable
     - 7.3 Index freshness

     Done, but plan0-2-0.md called these "not started" or "partial" — verified
     directly against the code on main, corrections in the Evidence column

     ┌──────────────────────────┬────────────────────────────────────────────────────────────────────────────────────────────────────┐
     │           Item           │                                              Evidence                                                │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.1 Reposition README    │ README.md:3 leads with "When one coding agent stops, the next one continues the work." — the old lead │
     │                          │ is gone.                                                                                              │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.2 Verification         │ session.Checkpoint carries Verified and Blockers (internal/session/checkpoint.go:44-45); Commands    │
     │ primitive                │ folded in alongside them. Subsumes 7.2, which plan0-2-0.md separately called partial.                 │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.4 Worktree awareness   │ TestWorktreeScopesDoNotShareContinuity, TestSafeScopeKeepsTheWorktreeInsideItsProject               │
     │                          │ (internal/session/session_test.go) — worktrees no longer share one project's continuity.              │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.6 Demote general       │ cmd/brain/main.go — usage() shows the three journeys plus setup/doctor/mcp; helpAll() holds the      │
     │ surface                  │ rest, reached by `brain help all`.                                                                    │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.2 LICENSE              │ Apache 2.0, not MIT — changed since the old plan's audit, not an open item.                          │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.3 Release              │ Tags v0.1.4 through v0.1.8 exist and are published; docs/index.html's five download links and         │
     │                          │ SHA256SUMS reference v0.1.8 and were verified live in the prior session (CI run 33834733689).         │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.4 npm                  │ Published as @noeton/logos (renamed from @ankrainc), six packages, verified via `npx -y            │
     │                          │ @noeton/logos@0.1.8 version` on a clean cache in the prior session.                                   │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 7.4 git-history          │ internal/bootstrap/bootstrap.go — seeds a cold vault from git history already on disk (commit         │
     │ bootstrap                │ counts, ratios, dates, names; no model call), wired at cmd/brain/bootstrap.go.                        │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 6.1 Agent identity       │ memory.Memory.Agent (internal/memory/memory.go:63) — "names which coding agent learned this".        │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 7.5 Pin/never-include    │ memory.Pin / memory.Exclude (internal/memory/memory.go:794,805), wired to MCP pin_memory /            │
     │                          │ exclude_memory tools.                                                                                 │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 7.6 Vault continuity     │ session.AllContinuity (internal/session/continuity.go:98) backs `brain continuity`.                   │
     │ report                   │                                                                                                        │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ Stage 4.1 Quarantine     │ internal/memory/quarantine.go — quarantined memories are a normal row with a flag, surfaced by        │
     │                          │ `brain review` alongside rollup proposals (feat/consent-quarantine, merged).                          │
     ├──────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ Stage 4.2 Ask-before-    │ app/chat.go:57 gates agent.Learn behind consent.Allowed(); internal/consent/consent.go backs Grant/    │
     │ learning                 │ Revoke/Remaining, wired to App.GrantLearning/RevokeLearning. Landed in 0731f1d, well before f0c9c25 —  │
     │                          │ plan0-2-0.md's audit was wrong about this from the start, not something that landed since.            │
     └──────────────────────────┴────────────────────────────────────────────────────────────────────────────────────────────────────┘

     Partially done (re-checked, still accurate)

     - 1.7 Design docs — moved to systemmd/DESIGN.md (out of yap files/, as
       plan0-2-0.md wanted), but still describes clipboard capture and periodic
       screenshot + OCR (systemmd/DESIGN.md:140,143) as if built. Neither
       exists in app/. Uncorrected, just relocated.

     Not started (re-checked directly, genuinely open)

     - Stage 3 (privacy centre) — grepped app/*.go for CaptureStatus,
       StartCapture, PauseCapture, SetPolicy, DeleteRange, ExportVault: zero
       matches. feat/terminal-app (merged) redesigned the desktop panel's look,
       not this API surface — the two should not be confused.
     - 6.2 MCP resources — no resources.go in internal/mcpserver.
     - 2.5 Homebrew — no Formula/.
     - 2.6 Nix — no flake.nix.
     - Stage 5 — 5.1-5.8, 5.10, 5.11, 5.13 (5.9 and 5.12 remain done).

     Landed but never in either plan

     - Everything plan0-2-0.md listed here, plus: the friction/onboarding pass
       from the 2026-09-03/04 sessions — `brain resume` on an empty vault, an
       open-but-empty session no longer failing doctor, continuity no longer
       reading uncommitted.md as a checkpoint, the cross-process vault lock.
       These came out of bughunting, not this plan, and are tracked on
       bughunt/0.2.0 rather than here.

     ---

     What to do next

     Only two tiers remain; everything plan0-2-0.md called Tier 1 and most of
     Tier 2 is done.

     Tier A — the real gaps toward 0.2.0

     1. 1.7 DONE (2f0c671) — corrected DESIGN.md's capture section, and the
        misleading comment in internal/capture/sources/screen.go it was
        transcribing. Turned out more nuanced than either plan expected:
        frontmost/browser/calendar/git are built AND wired into `brain capture
        --daemon`; clipboard has no source at all; screenshot+OCR is a real,
        working primitive with zero callers anywhere in cmd/ or app/.
     2. 0.8 DONE (e953b5d) — an ExitPlanMode PostToolUse hook now saves the
        approved plan text as its own vault note under
        sessions/<project>/plans/, alongside (not instead of) the existing
        activity-log row, so it survives the log's 30-day capture retention
        and is reachable by `why`/`recall` once indexed. `brain plans
        <project>` is the visibility surface, matching the precedent
        `activity` already set for a silent-by-design capture hook. Filenames
        are prefixed "plan-", not a bare timestamp, so they can never satisfy
        IsCheckpointFile and be misread as a checkpoint — proved directly by
        TestASavedPlanIsNotMistakenForACheckpoint and
        TestPlanFilenameNeverMatchesACheckpoint. Folding plan text into
        Checkpoint's own schema was considered and deliberately not done: a
        plan and the checkpoint that later closes its session are different
        things with different lifetimes, and a standalone note meets the
        goal (indexed, durable, reachable) without touching a schema every
        existing checkpoint has to keep parsing.

     Tier B — as originally sequenced, unchanged from plan0-2-0.md except 4.2
     removed (see status ledger above — it was already done, plan0-2-0.md's
     audit was wrong about it from the start)

     3. Stage 3 privacy centre (CaptureStatus, StartCapture, PauseCapture,
        SetPolicy, DeleteRange, ExportVault).
     4. 6.2 MCP resources.
     5. 2.5 Homebrew, 2.6 Nix.

     ---

     0.8 — Persist plan-mode plans into the vault (carried from plan0-2-0.md)

     Every checkpoint today records what was tried and what came of it, never
     what was proposed before the work started. Claude Code's plan mode already
     produces that text — a concrete, reviewed plan the user approved before any
     file changed — and today it is discarded the moment the session that wrote
     it ends. The next agent to resume sees the checkpoint's `next` field, one
     sentence, instead of the plan that sentence was drawn from.

     Save that plan text — as its own vault note (so it is indexed and
     reachable by `why`/`recall`) and/or folded into the checkpoint that closes
     the session it belonged to. Open questions to settle before implementing:
     whether this is a new MCP tool the plan-mode flow calls directly, a hook on
     ExitPlanMode, or a field added to session.Checkpoint alongside
     Verified/Blockers; and whether a plan that was approved but abandoned
     mid-way should be marked as such rather than read back as still current.

     ---

     Verification

     Unchanged from plan0-2-0.md for genuinely open items:

     - brain --help fits one screen; brain help all still lists all verbs.
       (Passes — re-verified above.)
     - go test ./..., go test -tags chaos ./chaos/..., gofmt -l ., go vet ./...
       clean. (Passes as of 812c4e9.)
     - Items still to prove once built: Stage 3's six functions exist and are
       wired to app.go; app/chat.go asks before agent.Learn runs; a plan-mode
       plan round-trips through resume the same way a checkpoint does.

     ---

     Open thread — the authentic-mode benchmark rerun

     Unchanged from plan0-2-0.md. Not re-audited this revision; carried forward
     as still open.

     Out of scope (unchanged)

     curl | sh, macOS notarization, a permissions engine, multi-agent leases, and
     any expansion of the secretary/voice/presence/dream/capture verticals. Temporal
     remains the one benchmark family where a competitor wins and deserves its own
     pass.
