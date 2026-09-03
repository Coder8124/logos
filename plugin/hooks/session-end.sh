#!/usr/bin/env bash
#
# Record that a session ended, and whether it ended cleanly.
#
# The deliberate limitation: a hook cannot write a good checkpoint. It has no
# idea what was decided or what failed — only the model knows that, and by the
# time this runs the model is gone. Writing a fabricated summary here would be
# worse than writing nothing, because a checkpoint nobody can trust is a
# checkpoint that gets ignored, and then so are the real ones.
#
# What a hook *can* do is close the loop honestly: note that work happened and
# was never committed. That is a fact, it needs no model, and it turns "the agent
# forgot to checkpoint" from something invisible into something the next session
# and `logos doctor` can both see.
#
# And it says so on the way out. This used to send every byte to /dev/null,
# which meant the one moment Logos does its quietest and most important job —
# recording that a session happened at all — looked exactly like Logos doing
# nothing. A product whose work is invisible is a product users assume is not
# working. One line on stderr, and one line when it fails, is the whole fix.
set -uo pipefail

. "$(dirname "${BASH_SOURCE[0]}")/../bin/resolve.sh" 2>/dev/null || exit 0
logos_resolve || exit 0

project=$(logos_project "${CLAUDE_PROJECT_DIR:-$PWD}")
[ -z "$project" ] && exit 0

# A note, not a checkpoint, and it states only what this hook actually knows:
# that a session ended, and when.
#
# It deliberately does not say "ended without checkpointing" — a hook cannot see
# whether the model committed one, and asserting it either way would be a claim
# with nothing behind it. The absence of a checkpoint after this note is itself
# the signal, and it is one `logos doctor` can read off the vault without
# anybody having to guess.
if "${LOGOS[@]}" note "$project" "claude-code session ended" >/dev/null 2>&1; then
  echo "Logos: session on \"$project\" recorded. Next session resumes from here." >&2
else
  # Named, not swallowed. A vault that cannot be written to is a continuity
  # layer that has stopped working, and the user finding that out tomorrow —
  # when the handoff they were counting on is not there — is the expensive way
  # to learn it. Still exit 0: reporting the failure must not become the
  # failure.
  echo "Logos: could not record the end of this session on \"$project\" — run 'logos doctor'." >&2
fi

exit 0
