#!/usr/bin/env bash
#
# Walk the first-run surface the way a new user meets it, against a throwaway
# home directory.
#
#   ./scripts/first-run.sh
#   LOGOS_RUNTIME=off ./scripts/first-run.sh    # as a machine with no model runtime
#
# Every first-run defect this repository has shipped was invisible from the
# developer's own laptop, because that laptop already has a vault, a recorded
# vault pointer, four connected hosts and a warm index. This script takes all
# of that away: a fresh HOME, a fresh XDG_CONFIG_HOME, and PATH cut down to
# /usr/bin:/bin so no real host CLI is reachable.
#
# That last part is a safety rule, not a tidiness one. `logos setup` shells out
# to `claude mcp add` and `codex mcp add`, and `chooseVault` records a vault
# pointer that the desktop app reads. A walk that leaks out of the fake home
# repoints the developer's own machine — which has happened, and had to be
# undone by hand. Nothing here runs outside HOME.
#
# What it cannot prove: a real TTY, hosts actually connecting, and an install
# from a published npm tarball. Those need a second user account. This is the
# part that can be automated, run before that.
#
# A model runtime is not in HOME: discovery probes localhost:11434 and the three
# ports beside it (internal/provider), so a developer with Ollama running sees
# the with-a-runtime path whatever HOME says. LOGOS_RUNTIME=off is passed
# through to every command, and is how to walk the no-runtime first run — the
# common one — on a machine that has one.
set -uo pipefail

cd "$(dirname "$0")/.."
BIN=$(mktemp -d)/logos
go build -o "$BIN" ./cmd/logos || exit 1

HOME_DIR=$(mktemp -d)/fresh
mkdir -p "$HOME_DIR"
trap 'rm -rf "$(dirname "$HOME_DIR")"' EXIT

# A host detected by directory, not by CLI: Claude Desktop and Cursor are found
# this way, so setup has something to register against without any binary being
# on PATH.
mkdir -p "$HOME_DIR/Library/Application Support/Claude" "$HOME_DIR/.cursor"

# env -i, not just HOME=: an inherited LOGOS_VAULT would silently point the whole
# walk back at the developer's real vault.
run() {
	local what=$1
	shift
	printf '\n\033[1m$ logos %s\033[0m\n' "$what"
	env -i HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" \
		PATH=/usr/bin:/bin TERM="${TERM:-dumb}" ${RUNTIME:+LOGOS_RUNTIME="$RUNTIME"} "$BIN" "$@"
	printf '\033[2m[exit %d]\033[0m\n' $?
}

# Read before env -i throws it away.
RUNTIME=${LOGOS_RUNTIME:-}

echo "fake home: $HOME_DIR"
[ -n "$RUNTIME" ] && echo "LOGOS_RUNTIME=$RUNTIME"

# Before setup: every verb a curious user might type first. None of them should
# build half a vault, and each should say what to do instead.
run "resume" resume
run "doctor" doctor
run "search anything" search anything

run "setup --dry-run" setup --dry-run
run "setup --yes" setup --yes

# After setup: the vault exists, so these are the real empty-state messages
# rather than the missing-vault gate.
run "resume" resume
run "note \"started on auth\"" note "started on auth"
run "context \"add auth to the api\"" context "add auth to the api"
run "tried \"rewriting the parser\"" tried "rewriting the parser"
run "doctor" doctor

printf '\n\033[1mhost configs written\033[0m\n'
find "$HOME_DIR" -name '*.json' -o -name '*.toml' | sed "s|$HOME_DIR|~|"

# A backup beside a config nothing changed is litter in the user's own config
# directory, and setup is run more than once — after moving a vault, after an
# update, or just to check.
run "setup --yes (second run)" setup --yes
printf '\n\033[1mbackups left behind (want: none)\033[0m\n'
find "$HOME_DIR" -name '*.logos-backup' | sed "s|$HOME_DIR|~|"
