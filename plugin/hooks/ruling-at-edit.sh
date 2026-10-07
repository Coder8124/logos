#!/usr/bin/env bash
#
# Show a recorded ruling to an agent about to edit a file the ruling names.
#
# Runs before every Edit, Write and MultiEdit, so it keeps record.sh's rules:
# never fail, never block. The edit always goes ahead — this only adds one
# line of context beside it, once per file per session, and nothing at all
# when no ruling names the file. All of the deciding is in Go
# (`logos hook claude-code pre-edit`), which is held to one indexed lookup and
# gives up silently after 3s.
#
# A logos older than the edit hook rejects the subcommand; that error goes to
# /dev/null and the edit proceeds as it would have without Logos.
set -uo pipefail

. "$(dirname "${BASH_SOURCE[0]}")/../bin/resolve.sh" 2>/dev/null || exit 0
logos_resolve || exit 0

# stdin is the host's payload, passed through untouched.
"${LOGOS[@]}" hook claude-code pre-edit 2>/dev/null || true
exit 0
