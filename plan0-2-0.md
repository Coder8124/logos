Continuity for coding agents — status and what remains

     Context

     This plan was written to take brain/Logos from "works, but nobody can install
     it or verify it" to a shippable coding-agent continuity layer. The question
     being answered now is "have we completed this plan?"

     No. Roughly half. Everything that removes a false claim or an install
     barrier is done. Almost nothing that adds new product surface is.

     The split is not accidental — it follows the plan's own sequencing advice
     ("if this needs to shrink, cut from the bottom"), and the bottom is where the
     uncut work sits. But three items near the top of the revised order
     (0.2, 0.4, 0.6) were skipped over, and those are the real gaps.

     Audit performed against the tree at f0c9c25 on main.

     ---

     Status ledger

     Done and verified

     ┌─────────────────────────────────┬──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
     │              Item               │                                                                      Evidence                                            │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.1 MCP serves with no runtime  │ router.ErrNoRuntime; internal/mcpserver/nomodel_test.go                                                                  │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.2 CLI degrades too            │ search/context/resume fall through to lexical                                                                            │
     ├─────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.2b Capture retention          │ RetentionDays, 30-day default, capture.Footprint                                                                                                   │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.3 Ask before wiring           │ cmd/brain/setup.go — --dry-run, --host, --yes, per-host confirm                                                          │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.4 Doctor says "unknown"       │ internal/health/health.go — 8 checks (vault, notes, index, embeddings, model runtime, contts), ok/failed/unchecked tally │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.5 Setup proves the vault      │ doctorIntegration() at cmd/brain/main.go:453                                                                             │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.6 Decision-grade model prompt │ --all-models, sizes printed, T1/T2 skipped by default                                                                                              │
     ├─────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.1 Module path                 │ go.mod = github.com/Coder8124/brain; go install …@latest verified from a scratch GOPATH                                  │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.2 LICENSE                     │ MIT, present, travels in archives                                                                                        │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.3 Git state without the model │ internal/gitstate/ — Read(), branch/commit/dirty/diffstat, wired to session.Checkpoint.Git                               │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.5 Repo → project              │ internal/mcpserver/scope.go + memory.AllInProject, 10 tests                                                                                        │
     ├─────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 5.9 First run is a handoff      │ setup closes on a demonstrated handoff                                                                                   │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 5.12 Continuity observable      │ health.go:294 — "last checkpoint N ago — project, by agent"                                                              │
     ├─────────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 7.3 Index freshness             │ folded into the index + embeddings health checks                                                                         │
     └─────────────────────────────────┴──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

     Partially done

     ┌────────────────────────┬─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
     │          Item          │                                                                    What is missing                                          │
     ├────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.1 Reposition README  │ README.md:3 still leads with "The memory you own." — the exact line 0.1 said to retire. The six evi the body, not the lead. │
     ├────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 0.4 Worktree awareness │ gitstate.State.Worktree is read and tested, but nothing scopes on it — a worktree is still not a di                         │
     ├────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 1.7 Design docs        │ DESIGN.md is out of the root but still in yap files/ describing clipboard capture and screenshot OCR that do not exist. Uncorrected, just relocated.  │
     ├────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.3 Release            │ .github/workflows/release.yml exists; no v0.1.0 tag, no published release. The site links to releas                         │
     ├────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 2.4 npm                │ Six packages build and pass scripts/test.js under @ankrainc. None published — blocked on npm 2FA.                           │
     ├────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
     │ 7.2 Richer checkpoints │ The git half landed (0.3). The narrative half — commands, verified, blockers — did not.                                     │
     └────────────────────────┴─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

     Not started

     - 0.2 Verification as a primitive — no Commands/Verified/Blockers on
       session.Checkpoint, no "safe to continue" block. This was second in the
       revised order and is the item that makes a resumed agent able to act safely.
     - 0.6 Demote the general surface — usage() still lists 40 verbs; there
       is no brain help all to hide them behind.
     - 2.5 Homebrew — no Formula/.
     - 2.6 Nix — no flake.nix.
     - Stage 3 (privacy centre) — app/app.go has zero of CaptureStatus,
       StartCapture, PauseCapture, SetPolicy, DeleteRange, ExportVault.
     - Stage 4 — 4.1 quarantine (MCP still writes straight to the vault),
       4.2 ask-before-learning (app/chat.go:48 still calls agent.Learn
       unconditionally).
     - Stage 5 — 5.1–5.8, 5.10, 5.11, 5.13 (5.9 and 5.12 done).
     - Stage 6 — 6.1 agent identity on memories, 6.2 MCP resources
       (resources.go absent).
     - Stage 7 — 7.1 abandonment detection (session.Uncommitted exists but is
       not surfaced as abandonment), 7.4 git-history bootstrap, 7.5 pin/never-include,
       7.6 vault continuity report.

     Landed but never in this plan

     - The Logos rename and two-name seam (product vs development name).
     - npm packaging under @ankrainc, wrapper + 5 platform packages.
     - GitHub Pages site, live at https://coder8124.github.io/logos/.
     - The continuity benchmark and its paper (docs/continuity-benchmark.md),
       including authentic-mode measurement of mem0 and Letta.
     - The repo rename to Coder8124/logos.

     ---

     What to do next

     Three tiers. The first is small and unblocks the product's public story; the
     rest is the plan's original ordering with the completed items removed.

     Tier 1 — finish shipping (small, high value)

     1. Publish npm. Blocked only on a 2FA OTP. Six packages, platform-first.
     2. Tag and publish v0.1.0. The download page links five archives that
        currently 404 — the site makes a promise the repo does not keep. This is the
        same class of defect the whole plan exists to remove.
     3. Retire "The memory you own" as the README lead (0.1). One paragraph, and
        the six measured claims are already written elsewhere in the file.
     4. Correct yap files/DESIGN.md (1.7) — cut or explicitly mark the
        clipboard/screenshot/OCR section as unbuilt before the file returns to root.

     Tier 2 — the skipped top of the order

     5. 0.2 Verification on the checkpoint. Add Commands, Verified,
        Blockers to session.Checkpoint (internal/session/checkpoint.go:23) —
        additive, the vault note schema already takes string arrays. Render a
        "safe to continue" block ahead of the prose in contextpack/render.go.
        Subsumes 7.2.
     6. 0.6 Demote the general surface. usage() shows the three journeys plus
        setup/doctor/mcp; everything else moves behind brain help all.
        Removes no commands.
     7. 0.4 Worktree as a continuity context. gitstate already reads
        Worktree; make it scope sessions, so parallel worktrees on one repo stop
        sharing one handoff.

     Tier 3 — as originally sequenced

     8. 7.4 git-history bootstrap (cold start), then 6.1 agent identity, then
        Stage 4 consent, then Stages 3 and 5.

     ---

     Verification

     Against the plan's own list: items 1, 3, 6 pass (no-model path, go install
     from a scratch GOPATH, doctor honesty). Items 2, 4, 5, 7, 8, 9 are untested;
     item 7 (quarantine) cannot pass because the feature does not exist.

     For the Tier 1 and 2 work:

     - npm view @ankrainc/logos version returns 0.1.0, and
       npx -y @ankrainc/logos mcp serve completes an initialize handshake on a
       machine with no Go toolchain.
     - Every archive linked from docs/index.html returns 200 and its checksum
       matches SHA256SUMS.
     - A checkpoint carrying verified round-trips through resume and appears in
       the "safe to continue" block; existing checkpoints without it still render.
     - brain --help fits one screen; brain help all still lists all 40 verbs.
     - go test ./..., go test -tags chaos ./chaos/..., gofmt -l ., go vet ./...
       clean.

     ---

     Open thread — the authentic-mode benchmark rerun

     Separate from the plan above, and unresolved.

     The full authentic run (MEM0_INFER=1 LETTA_AGENT_LOOP=1) was killed because it
     was projected at tens of hours. The cause was measured and is not VRAM
     thrashing: latency scales with context at ~2 s per 1k tokens, and context grows
     ~6× within a scenario, so cost is quadratic in a scenario's event count.

     Two fixes were attempted:

     1. OLLAMA_BASE_URL — found and documented (f0c9c25). Without it letta
        starts, answers /v1/health/, serves a stale model list from Postgres, and
        rejects every handle with must be one of []. With it, ollama/qwen3:4b
        is discovered.
     2. A 4B model for letta — motivated by both speed and fairness: letta was
        running glm-4.7-flash (29.9B) against mem0's gemma3:4b, which is not
        like-for-like and favours letta.

     The qwen3:4b probe is invalid. Letta's agent loop aborted with
     NoResultFound: Step with id … does not exist / Run not found, so it wrote
     nothing and scored 0 tokens — an error, not a measurement. Scenario
     supersession-current-value took 529 s across all nine systems.

     Next step is a discriminator, not another long run: re-run the same single
     scenario with LETTA_MODEL=ollama/glm-4.7-flash:latest against the current
     server. If glm also returns 0 tokens, the restart broke the letta schema and
     the model choice is innocent; if glm scores 50% fidelity as before, qwen3:4b
     drives letta down a code path it cannot complete and a different small
     tool-capable model is needed.

     Do not launch the full suite until one scenario has a measured per-event cost.
     Three estimates have now been wrong (~7 h, ~38 h, and the thrashing diagnosis).

     Added 2026-09-04 — capture the plan, not just the outcome

     Every item above is added after a session ends: a checkpoint records what
     was tried and what came of it, but not what was proposed before the work
     started. Claude Code's plan mode already produces that text — a concrete,
     reviewed plan the user approved before any file changed — and today it is
     discarded the moment the session that wrote it ends. The next agent to
     resume sees the checkpoint's `next` field, one sentence, instead of the
     plan that sentence was drawn from.

     0.8 Persist plan-mode plans into the vault. When an agent exits plan mode
     with an approved plan, save that plan text — as its own vault note (so it
     is indexed and reachable by `why`/`recall`) and/or folded into the
     checkpoint that closes the session it belonged to. Open questions to
     settle before implementing: whether this is a new MCP tool the plan-mode
     flow calls directly, a hook on ExitPlanMode, or a field added to
     session.Checkpoint alongside Verified/Blockers; and whether a plan that
     was approved but abandoned mid-way should be marked as such rather than
     read back as still current.

     Out of scope (unchanged)

     curl | sh, macOS notarization, a permissions engine, multi-agent leases, and
     any expansion of the secretary/voice/presence/dream/capture verticals. Temporal
     remains the one benchmark family where a competitor wins and deserves its own
     pass.
