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

cat <<EOF
Continuity from Logos — the previous session on "$project", including what was
already ruled out. Read the failed approaches before proposing anything; they
are there to stop you repeating work that has already been paid for.

$handoff
EOF
exit 0
