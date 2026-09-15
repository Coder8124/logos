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
# What a hook *can* do is close the loop honestly. When the session edited or
# ran things after its last checkpoint, logos writes an automatic checkpoint
# from the activity log — the first prompt, the files, the commands — labelled
# as not written by an agent and claiming nothing verified or ruled out. Those
# are facts, they need no model, and the next session starts from what was done
# rather than from a note that something was. Otherwise it notes that work was
# left open, which `logos doctor` can see too.
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

# When there is no automatic checkpoint to write, a note, and it states only what this hook actually knows:
# that a session ended, and when.
#
# It deliberately does not say "ended without checkpointing" — a hook cannot see
# whether the model committed one, and asserting it either way would be a claim
# with nothing behind it. The absence of a checkpoint after this note is itself
# the signal, and it is one `logos doctor` can read off the vault without
# anybody having to guess.
#
# Only when work is still open, and not twice in a row: after a checkpoint this
# line made a good handoff look stale, and sessions that never checkpointed
# stacked identical copies of it. logos decides both (see runNote); an older
# logos ignores the variable and notes as it always did.
#
# LOGOS_AUTO_CHECKPOINT asks for the automatic checkpoint above; logos reads the
# session id from this hook's payload, which reaches it on the inherited stdin.
# An older logos ignores the variable and falls back to the note.
#
# LOGOS_AGENT because the CLI otherwise signs as "cli", and this note is the
# Claude Code session's, under the name its MCP calls already carry. The BRAIN_
# names too, because the resolver still accepts a 0.4 brain binary, which reads
# only those.
if out=$(LOGOS_AGENT=claude-code LOGOS_NOTE_IF_UNCOMMITTED=1 LOGOS_AUTO_CHECKPOINT=1 BRAIN_AGENT=claude-code BRAIN_NOTE_IF_UNCOMMITTED=1 "${LOGOS[@]}" note "$project" "claude-code session ended" 2>/dev/null); then
  case "$out" in
    auto*)    echo "Logos: session on \"$project\" saved as an automatic checkpoint (not written by the agent, unverified) — the next session resumes from what ran." >&2 ;;
    skipped*) echo "Logos: session on \"$project\" ended — nothing new to flag for the next session." >&2 ;;
    *)        echo "Logos: session on \"$project\" ended without a checkpoint — the next session will see work was left open, not what it was." >&2 ;;
  esac
else
  # Named, not swallowed. A vault that cannot be written to is a continuity
  # layer that has stopped working, and the user finding that out tomorrow —
  # when the handoff they were counting on is not there — is the expensive way
  # to learn it. Still exit 0: reporting the failure must not become the
  # failure.
  echo "Logos: could not record the end of this session on \"$project\" — run 'logos doctor'." >&2
fi

exit 0
