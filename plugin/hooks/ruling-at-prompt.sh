#!/usr/bin/env bash
#
# Show a recorded ruling beside a prompt that restates an approach already
# ruled out in its project.
#
# Runs on every prompt, so it keeps record.sh's rules: never fail, never
# block. The prompt always goes through — this only adds context beside it,
# each ruling once per session, and nothing at all when the prompt restates
# none. All of the deciding is in Go (`logos hook claude-code prompt`), which
# uses no model and gives up silently after 3s.
#
# A logos older than the prompt hook ignores the event and prints nothing.
set -uo pipefail

. "$(dirname "${BASH_SOURCE[0]}")/../bin/resolve.sh" 2>/dev/null || exit 0
logos_resolve || exit 0
# Tells logos it has a hook's few seconds: the index switch waits for a command
# nobody is about to kill.
export LOGOS_HOOK=1

# stdin is the host's payload, passed through untouched.
"${LOGOS[@]}" hook claude-code prompt 2>/dev/null || true
exit 0
