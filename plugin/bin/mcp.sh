#!/usr/bin/env bash
#
# Start the Logos MCP server, whichever way Logos happens to be installed here.
#
# plugin.json cannot express a fallback: `command` is one program name, and if
# that program is missing the host reports CONNECTION_CLOSED with no hint as to
# why. That is the worst failure this plugin has, because it is the one a user
# hits before they have any reason to trust the thing — so the resolution lives
# in a script that can try in order and say something useful when it cannot.
#
# The order matches hooks/session-start.sh deliberately: the hooks and the MCP
# server must talk to the *same* binary, or a session resumes from one vault and
# checkpoints into another.
set -uo pipefail

resolver="$(dirname "${BASH_SOURCE[0]}")/resolve.sh"
if ! . "$resolver" 2>/dev/null; then
  echo "logos: plugin is incomplete — $resolver is missing. Reinstall the plugin." >&2
  exit 1
fi

if logos_resolve; then
  exec "${LOGOS[@]}" mcp serve "$@"
fi

# Nothing to exec. Say so on stderr, where the host surfaces it, rather than
# dying silently and leaving the user with a connection error and no cause.
echo "logos: no logos or brain binary found on PATH or in the usual install directories," >&2
echo "logos: and @noeton/logos is not installable here." >&2
echo "logos: install one — go install github.com/Coder8124/brain/cmd/brain@latest — then restart Claude Code." >&2
exit 1
