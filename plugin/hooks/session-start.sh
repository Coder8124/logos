#!/usr/bin/env bash
#
# Inject the last handoff at the start of a session, without being asked.
#
# This is the difference between continuity that works and continuity that works
# when the model remembers to ask for it. Every brain tool is available over MCP,
# but a tool is only called if the model decides to call it — and the one moment
# it most needs the previous agent's dead ends is the moment before it has any
# reason to suspect they exist.
#
# So this runs on SessionStart and puts the handoff in front of the model whether
# or not it would have thought to look.
#
# Rules this obeys, because a hook that misbehaves poisons every session:
#   - never fail. Exit 0 no matter what; a broken hook must not block work.
#   - never stall. Everything is bounded, and Logos needs no model for this.
#   - say nothing when there is nothing to say. An empty vault or a project with
#     no history prints nothing rather than noise.
#   - say it out loud when there *is* something to say. A hook that silently
#     improves an answer is, from the user's chair, indistinguishable from a
#     hook that never ran; a continuity layer nobody sees restore anything is a
#     continuity layer nobody believes in. So the block below opens with a
#     receipt and asks the model to hand that receipt to the user, once.
set -uo pipefail

# Resolve the binary without requiring it on PATH: an npx-installed plugin has
# no binary of its own, and a globally installed one does. The npm wrapper
# installs both names; a source build installs only the development one.
if command -v logos >/dev/null 2>&1; then
  LOGOS=(logos)
elif command -v brain >/dev/null 2>&1; then
  # The development name, which the binary and a source build still use.
  LOGOS=(brain)
elif command -v npx >/dev/null 2>&1; then
  LOGOS=(npx -y @noeton/logos)
else
  exit 0
fi

# The project is the directory being worked in. This is the assumption a coding
# agent makes anyway, and it is why the cwd is the right key: a repo is a
# project, and the agent is already standing in it.
project=$(basename "${CLAUDE_PROJECT_DIR:-$PWD}")
[ -z "$project" ] && exit 0

# Bounded, and quiet on failure. A vault that does not exist yet, a project with
# no checkpoints, or a Logos that is not installed all land here and print
# nothing.
handoff=$("${LOGOS[@]}" resume "$project" 2>/dev/null) || exit 0
[ -z "$handoff" ] && exit 0

# resume on a project with no checkpoint still returns context, and says so.
# That is useful to a person and noise to a model that has just been handed the
# repo, so only inject when there is an actual handoff.
case "$handoff" in
  *"no checkpoint yet"*) exit 0 ;;
esac

# What the receipt is allowed to claim, counted off the handoff itself rather
# than guessed at.
carried=$(printf '%s\n' "$handoff" | awk '
# Reads a context pack on stdin and prints one line naming what it carries.
#
# The markers are the literal strings internal/contextpack/render.go writes, and
# a count is the run of "- " bullets under one of them. Counting anything looser
# — every bullet in the pack, say — would inflate the receipt, and a receipt
# that overstates what was restored is worse than none: the user checks it once,
# finds half the promised items missing, and never reads it again.
/^\*\*Verified — safe to continue from:\*\*/  { k = "1verified fact";        next }
/^\*\*Known broken — do not build on it:\*\*/ { k = "2known blocker";        next }
/^## Not settled/                             { k = "3open question";        next }
/^## Recorded since, not yet checkpointed/    { k = "4uncheckpointed note";  next }
/^(## |\*\*)/                                 { k = "";                      next }
k != "" && /^- /                              { n[k]++ }
END {
    m = 0
    for (k in n) key[++m] = k
    for (i = 1; i < m; i++) for (j = i + 1; j <= m; j++)
        if (key[j] < key[i]) { t = key[i]; key[i] = key[j]; key[j] = t }
    for (i = 1; i <= m; i++) {
        c = n[key[i]]
        label = substr(key[i], 2)          # drop the sort prefix
        if (c != 1) label = label "s"
        out = out (out ? ", " : "") c " " label
    }
    print out
}
' 2>/dev/null) || carried=""
[ -z "$carried" ] && carried="the previous session's stopping place"

# Whose checkpoint, and how old. The most convincing line on the receipt,
# because it is the one claim the user can check against their own memory of
# when they last sat down — so it is lifted from the pack verbatim rather than
# recomputed here, where it could drift out of agreement with the very text
# sitting underneath it.
when=$(printf '%s\n' "$handoff" \
  | sed -n 's/^Last checkpoint by \(.*\)\.$/from \1/p' \
  | sed -e 's/\*\*//g' -e 's/, handed off to .*//' \
  | head -1) || when=""

cat <<EOF
Continuity from Logos — the previous session on "$project", including what was
already ruled out. Read the failed approaches before proposing anything; they
are there to stop you repeating work that has already been paid for.

FIRST, in one short line before anything else, tell the user that Logos restored
context for "$project"${when:+ $when}, carrying $carried. They cannot see this
block, and a restore they never hear about reads to them as a restore that never
happened. One line, then get on with the work.

$handoff
EOF
exit 0
