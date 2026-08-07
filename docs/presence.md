# feat/presence — the Secretary as a presence — step 9

Not a dashboard. A dashboard is a place you *go*; this is a presence that comes to
you. The Secretary already leads — it opens with a brief instead of waiting to be
asked — so the natural end of that idea isn't another screen, it's an ambient,
named, conversational presence: it greets you with what matters, answers from your
memory, and speaks up when your calendar, your agenda, or something you meant to
remember needs you. The dashboard, if it ever ships, becomes the glanceable
fallback behind this.

Almost every organ already exists. This step is mostly wiring them into one voice.

- **Anticipation** → `secretary.Compose` (the brief: upcoming meetings, open loops,
  dormant work, "you usually do X"), `routine` anomalies, `dream` insights.
- **A voice** → `internal/voice`: whisper.cpp in, Piper out, `SpeakStream` already
  speaks a reply sentence-by-sentence as it generates.
- **Continuity** → the whole memory platform. The thing a stateless assistant
  (Siri, Alexa, a movie AI) structurally cannot do: it actually knows you.
- **A persona** → `internal/flavor` (`.brain/flavor.json`), where the name will live.
- **Hands** → the `action` confirmation gate.

## The governing law: augment, never override

The product's whole reason for being is *help you, not replace your brain*. The
presence makes that a hard contract, not a vibe. Six rules, each enforceable:

1. **It proposes; it never decides.** Every interjection ends open — "want me to
   draft it?" — never "I've done it." It fills the gaps in your memory and then
   gets out of the way of your reasoning.
2. **Hands stay behind the gate.** Nothing outward — an email, a booking, a file
   export — runs without `action.Approve`. The presence can *offer* to act; it
   cannot act. This is the line between this and an autonomous agent, and it is
   the line the product will not cross.
3. **It defers to your focus.** During deep, sustained focus it stays silent
   unless something is genuinely time-critical (a meeting in five minutes).
   Everything else waits for a natural break.
4. **Always dismissible, always silenceable.** "Not now" and "mute for an hour"
   always work, and a dismissed nudge does not re-fire.
5. **Evidence, not editorial.** It states the fact and cites its source ("you said
   you'd email Sarah three days ago — from Tuesday's note"), and never judges the
   choice ("you're behind"). Same discipline as proposal evidence.
6. **It never rewrites your conclusions.** It surfaces what you might want to
   recall or reconsider; it does not overwrite a decision you've made or argue you
   out of your own thinking. A reminder, not a supervisor.

If a behaviour can't be phrased as "here's what I noticed, over to you," it doesn't
belong in the presence.

## The name is the wake word

You give it a name; that name is how you address it and how you wake it.

- **Storage.** Add `Name string` to `flavor.Config` (`.brain/flavor.json`), beside
  `Active`/`ScreenNotes` — persona settings, kept out of the model/key config.
  `brain name <X>` sets it; empty means unnamed (push-to-talk still works, it just
  has no spoken wake word yet). The name also personalises the flavor's greeting
  and self-reference.
- **Detection is pluggable, and honest about limits.** whisper.cpp is a
  transcriber, not a wake-word engine, so a true always-listening wake word needs a
  small dedicated model (openWakeWord / porcupine-class). Resolve it the same way
  the other voice binaries resolve — env override → bundled resource → off — so the
  presence ships useful without it and gains the wake word when the model is
  present.
- **Push-to-talk is the default; wake word is opt-in.** Until a wake-word model is
  configured, you open a turn by hotkey. That keeps the first version real.
- **Privacy is structural, not promised.** When a wake-word model *is* on, it runs
  on-device over a short rolling audio buffer that is **never stored and never
  transmitted**. Nothing is transcribed until the name matches; whisper only starts
  *after* the wake. Ambient in *availability*, not in surveillance — consistent with
  the local-first spine and the deliberate opt-in that already gates screen capture.

## The ambient loop

Evolve `runVoiceChat` from a Q&A session you invoke into a resident presence.

- **States:** *idle* (waiting on wake word or hotkey) → *listening* (a turn, capped
  like today's `Listen`) → *answering* (streamed via `SpeakStream`, so speech starts
  before the text finishes) → back to idle. It also has an *interjecting* entry
  point the daemon can trigger (below).
- **It opens with the brief.** First contact in a session speaks the greeting and
  the one or two things that matter now, drawn straight from `secretary.Compose` —
  not a wall, the top of the list.
- **Grounded answers.** Questions route through the same memory + vault grounding
  the `agent` package already uses, so "what did I promise Sarah?" is answered from
  the store, not guessed.
- **Degrades gracefully.** No TTS → it types. No STT → hotkey + typed input. No
  wake-word model → push-to-talk. Each capability check already exists
  (`CanListen`, `CanSpeak`).

## The interjection engine — what it watches

A small, restrained watcher that turns three signals into spoken nudges. Selection
is arithmetic (thresholds, times, counts); the model only phrases. It lives in a
new `internal/presence` package and is driven from the capture daemon's loop,
beside the existing tickers.

- **Calendar.** `event.Calendar` already feeds `brief.Upcoming`. Trigger: a meeting
  inside the lead-time window (default 10 min) that hasn't been acknowledged →
  speak once. This is the one class allowed to break focus (rule 3).
- **Agenda.** Open commitments from `secretary` — one due today or overdue, or one
  that has surfaced repeatedly. Raised at a natural break, never mid-focus.
- **Things you meant to remember.** Pending `dream` insights, dormant projects
  (`routine` anomalies — "haven't touched the API repo in nine days"), and any
  explicit "remind me" the user set. These are the "you try to remember but never
  quite do" cases the presence exists to cover.

**Restraint is the feature.** The brief's discipline governs here too: stalest- or
soonest-first, evidence-cited, threshold-gated, and **rate-limited** — at most one
interjection per quiet window unless time-critical. A presence that talks too much
becomes Clippy, and Clippy is exactly the thing that overrides your attention.

```
capture daemon
  ├─ focus / pull / screen / dream tickers        (existing)
  └─ presence ticker (~1 min)                      (new)
        └─ presence.Check(db, now, focusState) → []Nudge
              calendar (imminent) · agenda (due) · remember (dream/dormant/explicit)
              → speak the top one, if any clears the gate and the rate limit
```

## Package & wiring

- New `internal/presence`: `Check(db, now, focus) ([]Nudge, error)` (pure selection
  over the signals above, reusing `secretary`, `routine`, `dream`), plus the
  rate-limit / acknowledged-nudge state (a small table, like `replay_state`).
- `internal/flavor`: add `Name`, and presence prefs (lead-time, quiet hours,
  interjections on/off).
- `internal/voice`: an optional `WakeBin`/`WakeModel` resolved like the rest; a
  `WakeWord` matcher when present.
- `cmd/brain`: `brain name <X>`; `brain presence` (run the ambient loop, evolving
  `runVoiceChat`); a presence ticker case in the capture daemon that calls
  `presence.Check` and speaks through `SpeakStream`.

## Config (`.brain/flavor.json`)

```json
{
  "active": "secretary",
  "name": "Friday",
  "presence": {
    "interjections": true,
    "wake_word": true,
    "meeting_lead_minutes": 10,
    "min_gap_minutes": 60,
    "quiet_hours": ["22:00", "08:00"]
  }
}
```

The rate limit is a **cooldown, not a quota**: the unit is one *unprompted spoken
interjection*, and `min_gap_minutes` is the minimum quiet gap between two
non-urgent ones — never a per-hour tally, never anything you initiate. Imminent
meetings are exempt.

## Reused vs new

| Reused | New |
|---|---|
| `secretary.Compose` (brief), `routine`, `dream` insights | the interjection watcher + its restraint/rate-limit state |
| `voice` STT/TTS/`SpeakStream`, capability checks | the ambient loop states; an optional wake-word matcher |
| `flavor` persona config | `Name` (= wake word) + presence prefs |
| `action` gate | nothing — the gate is used exactly as-is, on purpose |

## Decisions (resolved)

1. **Wake word: now.** Built in the first cut, over the existing whisper toolchain
   (a short rolling window is transcribed and matched against the name; a dedicated
   low-power engine can drop in behind `WakeHeard` later). `brain presence --wake`
   turns it on for a session without editing config.
2. **The loop runs in the CLI first** — `brain presence` — fast to iterate and
   testable; the Wails widget is the follow-up home.
3. **Restraint is a cooldown, not a quota** (see config above): one unprompted
   interjection at a time, non-urgent ones spaced by `min_gap_minutes`, only
   imminent meetings may break focus.
