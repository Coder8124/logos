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
if [ -n "${LOGOS_REJECTED:-}" ]; then
  echo "logos: found $LOGOS_REJECTED, but it does not run as Logos — another program by that name, or an npm install with no node on this app's PATH," >&2
fi
echo "logos: no working logos or brain binary found on PATH or in the usual install directories," >&2
echo "logos: and @noeton/logos is not installable here." >&2
# Homebrew and npm first, and go install last. The user who is here has no
# working Logos and may well have no toolchain either — Claude Code's own
# installer brings neither Node nor Go — so an answer that starts by requiring a
# third one is an answer they cannot take.
echo "logos: install one, then restart Claude Code:" >&2
echo "logos:   brew install coder8124/tap/logos-mcp   (macOS, Linux)" >&2
echo "logos:   npm i -g @noeton/logos                 (Windows, or anywhere with Node)" >&2
echo "logos:   go install github.com/Coder8124/logos/cmd/logos@latest   (if you have Go)" >&2
exit 1
