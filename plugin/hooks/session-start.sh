#!/usr/bin/env bash
#
# Inject the last handoff at the start of a session, without being asked.
#
# This is the difference between continuity that works and continuity that works
# when the model remembers to ask for it. Every logos tool is available over MCP,
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

# Resolve the binary. Shared with the other hooks and with the MCP server, so
# that all of them talk to the same vault; see bin/resolve.sh for why PATH on
# its own is not enough.
. "$(dirname "${BASH_SOURCE[0]}")/../bin/resolve.sh" 2>/dev/null || exit 0
# Nothing resolving is silent unless the user pointed somewhere: a LOGOS_BIN
# with a typo, alone on the machine, is the likeliest way to get it wrong, and
# the notice that names it lives past this line.
if ! logos_resolve; then
  if [ -n "${LOGOS_BIN_REJECTED:-}" ]; then
    echo "FIRST, tell the user in one short line (they cannot see this block): LOGOS_BIN does not run as Logos and no other copy was found, so Logos is off this session: fix that path or remove it from Claude Code's environment."
  elif [ -n "${LOGOS_REJECTED:-}" ]; then
    echo "FIRST, tell the user in one short line (they cannot see this block): Logos is off this session: $(logos_quote "$LOGOS_REJECTED") was found but does not run as Logos — an npm install with no node on Claude Code's PATH, or another program named logos."
  fi
  exit 0
fi
# Tells logos it has a hook's few seconds: the index switch waits for a command
# nobody is about to kill.
export LOGOS_HOOK=1

# Keep the plugin level with the binary, without anyone having to ask.
#
# Claude Code has no auto-update for plugins and never refreshes a third-party
# marketplace, so hooks installed once stay at that version against a server
# that has moved on. The check is bounded to once a day inside logos, and it is
# backgrounded here with both file descriptors closed onto /dev/null: a child
# still holding the hook's stdout would keep Claude Code waiting on output that
# is never coming, which is a stalled session start — the one thing this hook
# must never cause. So nothing is waited for, and what the last run did is read
# back from the stamp file below, one session later.
( "${LOGOS[@]}" plugin autoupdate >/dev/null 2>&1 & ) >/dev/null 2>&1
updated=$("${LOGOS[@]}" plugin autoupdate --notice 2>/dev/null) || updated=""

# The activity log records every prompt and tool call this session makes, and
# the same plugin install that placed this hook turned it on. A record the user
# never agreed to and is never told about is surveillance however careful the
# redaction, so logos says it once — the notice is empty every session after,
# and empty always once recording is off.
#
# It is also the first contact: a repository Logos has not seen is announced
# once, with the way out (`logos config deny`), and a denied one gets nothing.
recording=$("${LOGOS[@]}" activity notice "${CLAUDE_PROJECT_DIR:-$PWD}" 2>/dev/null) || recording=""

# A logos older than this plugin, or a dev build passed over for a numbered
# release, said where the user hears it. The resolver's own stderr line never
# reached anyone: Claude Code shows a hook's stdout to the agent and drops the
# rest. Said every session until it is fixed, because each one runs on it.
# Only stdout is the relay. The paths behind it go to stderr, which Claude
# Code drops for a hook that exits 0 — there for anyone running the hook by hand.
skew=$(logos_skew_notice | tr '\n' ' ' | sed 's/ $//') || skew=""

# A plugin that updated itself in silence is indistinguishable from one that
# did nothing, so the notice is said even in the sessions that have no handoff
# to fold it into — an empty vault, a project with no checkpoints.
announce_update() {
  if [ -n "$updated$recording$skew" ]; then
    cat <<EOF
FIRST, tell the user in one short line (they cannot see this block):${updated:+ $updated}${recording:+ $recording}${skew:+ $skew}
EOF
  fi
  exit 0
}

# The project the work is filed under. Usually the directory being worked in —
# a repo is a project and the agent is already standing in it — but a repo whose
# folder is not what the work is called says so in .logos-project, and
# logos_project is what reads it. See internal/scope.
project=$(logos_project "${CLAUDE_PROJECT_DIR:-$PWD}")
[ -z "$project" ] && announce_update

# Bounded, and quiet on failure. A vault that does not exist yet, a project with
# no checkpoints, or a Logos that is not installed all land here and print
# nothing.
# From inside the project, so resume can tell this repository's checkpoints
# from those of another repository with the same folder name.
#
# LOGOS_EMBED=off: what gets injected is the checkpoint, which is markdown and
# needs no vectors, but resume embeds its query three times on the way. A model
# runtime slow to answer — loading the embedding model behind a large one —
# took this past the 10 s hook timeout, and Claude Code killed the hook with no
# restore and no reason.
#
# LOGOS_AGENT and LOGOS_SESSION are for the usage ledger, and change nothing in
# the pack but who it is addressed to. The agent, so a checkpoint another agent
# wrote is counted as a handoff; the session, so the SessionEnd receipt can say
# which of the rulings handed over here this session then stayed away from. The
# id is in the hook's payload on stdin, read with a bound: a host that left
# stdin open would otherwise stall the start of every session.
payload=""
[ -t 0 ] || IFS= read -r -d '' -t 2 payload || true
session=$(printf '%s' "$payload" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
handoff=$(cd "${CLAUDE_PROJECT_DIR:-$PWD}" 2>/dev/null && LOGOS_AGENT=claude-code LOGOS_SESSION="$session" LOGOS_EMBED=off "${LOGOS[@]}" resume "$project" 2>/dev/null) || announce_update
[ -z "$handoff" ] && announce_update

# resume on a project with no checkpoint still returns context — standing
# memories and vault notes. That is useful to a person and noise to a model
# that has just been handed the repo, so only inject when there is an actual
# handoff, which is exactly when the pack carries this heading (see
# internal/contextpack/render.go, which writes it only alongside a checkpoint).
#
# Anchored to the start of a line, and testing for what a handoff *has* rather
# than for a phrase that means it has none. The blocklist this replaces matched
# "no checkpoint yet" anywhere in the pack — including inside a checkpoint that
# happened to quote the phrase, which is a checkpoint about this very hook. It
# suppressed itself, silently, and looked exactly like no handoff existing.
#
# internal/contextpack/untrusted.go already neutralises headings forged inside
# vault content, so a heading surviving to here is one the renderer wrote.
if ! printf '%s\n' "$handoff" | grep -q '^## Where we left off'; then
  announce_update
fi

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
# "didn.t": the program sits inside single quotes, so no literal apostrophe.
/^\*\*Already tried, didn.t work:\*\*/        { k = "0ruled-out approach";   next }
/^\*\*Verified — safe to continue from:\*\*/  { k = "1verified fact";        next }
/^\*\*Known broken — do not build on it:\*\*/ { k = "2known blocker";        next }
/^## Not settled/                             { k = "3open question";        next }
/^## Recorded since, not yet checkpointed/    { k = "4uncheckpointed note";  next }
/^(## |\*\*)/                                 { k = "";                      next }
# Notes folded into the checkpoint follow Verified under a plain line, not a
# heading; without this their bullets were counted as verified facts.
/^Recorded during the session:/               { k = "";                      next }
# The marker the SessionEnd hook writes is not work anyone recorded; counting it
# as a note would inflate the receipt with the hook talking about itself.
k ~ /uncheckpointed/ && /session ended$/      { next }
k != "" && /^- /                              { n[k]++ }
END {
    m = 0
    for (k in n) key[++m] = k
    for (i = 1; i < m; i++) for (j = i + 1; j <= m; j++)
        if (key[j] < key[i]) { t = key[i]; key[i] = key[j]; key[j] = t }
    for (i = 1; i <= m; i++) {
        c = n[key[i]]
        label = substr(key[i], 2)          # drop the sort prefix
        if (c != 1) label = label (label ~ /h$/ ? "es" : "s")
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
Continuity from Logos for "$project". This is this session's resume: read the
ruled-out approaches before proposing anything, and call context only for a
narrower task.

FIRST, tell the user in one short line (they cannot see this block) that Logos
restored context for "$project"${when:+ $when}, carrying $carried.${updated:+ Also: $updated}${recording:+ $recording}${skew:+ $skew}

$handoff
EOF
exit 0
