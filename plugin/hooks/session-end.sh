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
set -uo pipefail

if command -v logos >/dev/null 2>&1; then
  LOGOS=(logos)
elif command -v brain >/dev/null 2>&1; then
  # The development name, which the binary and a source build still use.
  LOGOS=(brain)
elif command -v npx >/dev/null 2>&1; then
  LOGOS=(npx -y @ankrainc/logos)
else
  exit 0
fi

project=$(basename "${CLAUDE_PROJECT_DIR:-$PWD}")
[ -z "$project" ] && exit 0

# A note, not a checkpoint, and it states only what this hook actually knows:
# that a session ended, and when.
#
# It deliberately does not say "ended without checkpointing" — a hook cannot see
# whether the model committed one, and asserting it either way would be a claim
# with nothing behind it. The absence of a checkpoint after this note is itself
# the signal, and it is one `logos doctor` can read off the vault without
# anybody having to guess.
"${LOGOS[@]}" note "$project" "claude-code session ended" >/dev/null 2>&1 || true

exit 0
