#!/usr/bin/env bash
#
# Record one host event in the activity log.
#
# This is the automatic half of the record. Everything else Logos knows was
# written because a model chose to write it — `remember`, `checkpoint`, a note.
# That is fine until the model does not choose, and the gap is invisible: a
# session that decided three things and recorded none looks exactly like a
# session where nothing happened.
#
# The host has no such discretion. It reports every prompt, tool call and turn
# whether or not the agent would have mentioned them, so what this hook writes
# cannot be forgotten — only switched off.
#
# It runs on the critical path of every single tool call, which sets the rules:
#   - never fail. Exit 0 unconditionally. A hook that errors on tool call #400
#     of a long session has cost the user far more than the line it lost.
#   - never block. No model, no network, no lock. One append to a file.
#   - say nothing. This one is the exception to Logos's "announce yourself"
#     rule, and deliberately: a line of output per tool call is not visibility,
#     it is noise, and it would bury the receipts that *do* matter. The
#     announcement for this feature is `brain activity`, which is where a person
#     goes to look, and the session-start receipt that says the log is running.
set -uo pipefail

event="${1:-}"
[ -z "$event" ] && exit 0

. "$(dirname "${BASH_SOURCE[0]}")/../bin/resolve.sh" 2>/dev/null || exit 0
logos_resolve || exit 0

project=$(logos_project "${CLAUDE_PROJECT_DIR:-$PWD}")

# stdin is the host's payload; it is passed through untouched and parsed in Go,
# where a malformed field costs a field rather than the whole line.
"${LOGOS[@]}" activity record --event "$event" --project "$project" >/dev/null 2>&1 || true
exit 0
