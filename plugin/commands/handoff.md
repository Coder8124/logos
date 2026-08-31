---
description: Hand this work to another agent or person, with everything they need to continue
---

Write a checkpoint to brain that hands this work over, then tell me it is done.

Call the `checkpoint` tool (or `handoff` if I named who is taking over). Fill in
every field you can — anything you leave out is lost, because the next agent
sees only this.

The field that matters most is **`failed`**: approaches tried that did not work,
and why. That is the expensive knowledge. Everything else can be re-derived by
reading the code; a dead end cannot, and without it the next agent will spend
its first hour rediscovering what this one already paid for.

Be specific enough to act on:

- **task** — what was being attempted, not the ticket title
- **state** — where things actually stand, including anything half-finished
- **decisions** — what was chosen *and why*, so it is not silently reversed
- **failed** — what was ruled out and the reason it was ruled out
- **questions** — what is still genuinely open
- **files** — what was touched
- **next** — the single next step, concrete enough to start on

If I gave you a name after the command, that is who is taking over — use
`handoff` with `to` set to them.

$ARGUMENTS
