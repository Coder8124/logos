#!/usr/bin/env bash
#
# Walk the first-run surface the way a new user meets it, against a throwaway
# home directory.
#
#   ./scripts/first-run.sh
#   LOGOS_RUNTIME=off ./scripts/first-run.sh    # as a machine with no model runtime
#   LOGOS_BIN=$(npm prefix -g)/bin/logos ./scripts/first-run.sh   # an installed build
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
# It exits non-zero when a command exits other than the way a first run should,
# or when a second setup leaves a backup, so CI can run it on clean macOS and
# Linux machines (the first-run job), against a source build and against the
# published npm package. What it still cannot prove: a real TTY, and hosts
# actually connecting.
#
# A model runtime is not in HOME: discovery probes localhost:11434 and the three
# ports beside it (internal/provider), so a developer with Ollama running sees
# the with-a-runtime path whatever HOME says. LOGOS_RUNTIME=off is passed
# through to every command, and is how to walk the no-runtime first run — the
# common one — on a machine that has one.
set -uo pipefail

# Made absolute before the cd below, which would otherwise resolve a relative
# LOGOS_BIN against the repository root rather than where it was typed.
if [ -n "${LOGOS_BIN:-}" ]; then
	LOGOS_BIN=$(cd "$(dirname "$LOGOS_BIN")" && pwd)/$(basename "$LOGOS_BIN") || exit 1
fi

cd "$(dirname "$0")/.."

# Not mktemp's default: on Linux that is under /tmp, and setup rightly refuses
# to record a vault in a temporary directory — so the walk wired no hosts and
# doctor failed, for a reason no real user's home has. (macOS hid it: env -i
# drops TMPDIR, so the guard never saw /var/folders.) The setup tests make
# their homes inside the repository for the same reason, and share its limit:
# a checkout that is itself under /tmp fails the same way.
# Exit on failure: an empty HOME_DIR would walk with HOME=/fresh.
mkdir -p .first-run || exit 1
HOME_DIR=$(mktemp -d "$PWD/.first-run/home.XXXXXX") || exit 1
HOME_DIR=$HOME_DIR/fresh
mkdir -p "$HOME_DIR/.path"
trap 'rm -rf "$(dirname "$HOME_DIR")"' EXIT

# LOGOS_BIN is the npm package's `logos`, a node script, so node has to be
# reachable from the cut-down PATH. Only node is linked in: putting its whole
# directory on PATH would bring along whatever host CLIs were installed beside it.
if [ -n "${LOGOS_BIN:-}" ]; then
	BIN=$LOGOS_BIN
	if node=$(command -v node); then
		ln -s "$node" "$HOME_DIR/.path/node"
	fi
else
	BIN=$(dirname "$HOME_DIR")/logos
	go build -o "$BIN" ./cmd/logos || exit 1
fi

# A host detected by directory, not by CLI: Claude Desktop and Cursor are found
# this way, so setup has something to register against without any binary being
# on PATH. Claude Desktop's directory is under Library on macOS and under
# ~/.config on Linux, whatever XDG_CONFIG_HOME says (claudeDesktopConfig).
mkdir -p "$HOME_DIR/Library/Application Support/Claude" "$HOME_DIR/.config/Claude" "$HOME_DIR/.cursor"

failures=()

# env -i, not just HOME=: an inherited LOGOS_VAULT would silently point the whole
# walk back at the developer's real vault.
run() {
	local want=$1 what=$2
	shift 2
	printf '\n\033[1m$ logos %s\033[0m\n' "$what"
	env -i HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" \
		PATH="$HOME_DIR/.path:/usr/bin:/bin" TERM="${TERM:-dumb}" ${RUNTIME:+LOGOS_RUNTIME="$RUNTIME"} "$BIN" "$@"
	local got=$?
	printf '\033[2m[exit %d]\033[0m\n' $got
	[ $got -eq "$want" ] || failures+=("logos $what exited $got, want $want")
}

# Read before env -i throws it away.
RUNTIME=${LOGOS_RUNTIME:-}

echo "fake home: $HOME_DIR"
echo "logos: $BIN"
[ -n "$RUNTIME" ] && echo "LOGOS_RUNTIME=$RUNTIME"

run 0 "version" version

# Before setup: every verb a curious user might type first. None of them should
# build half a vault, and each should say what to do instead — and exit 1, so a
# script that runs one before setup does not carry on as if it worked.
run 1 "resume" resume
run 1 "doctor" doctor
run 1 "search anything" search anything

run 0 "setup --dry-run" setup --dry-run
run 0 "setup --yes" setup --yes

# After setup: the vault exists, so these are the real empty-state messages
# rather than the missing-vault gate.
run 0 "resume" resume
run 0 "note \"started on auth\"" note "started on auth"
run 0 "context \"add auth to the api\"" context "add auth to the api"
run 0 "tried \"rewriting the parser\"" tried "rewriting the parser"
run 0 "doctor" doctor

printf '\n\033[1mhost configs written\033[0m\n'
find "$HOME_DIR" -name '*.json' -o -name '*.toml' | sed "s|$HOME_DIR|~|"

# A backup beside a config nothing changed is litter in the user's own config
# directory, and setup is run more than once — after moving a vault, after an
# update, or just to check.
run 0 "setup --yes (second run)" setup --yes
printf '\n\033[1mbackups left behind (want: none)\033[0m\n'
backups=$(find "$HOME_DIR" -name '*.logos-backup' | sed "s|$HOME_DIR|~|")
if [ -n "$backups" ]; then
	echo "$backups"
	failures+=("a second setup left $(echo "$backups" | wc -l | tr -d ' ') backup(s)")
fi

if [ ${#failures[@]} -gt 0 ]; then
	printf '\n\033[1;31mfirst run failed:\033[0m\n'
	printf '  %s\n' "${failures[@]}"
	exit 1
fi
printf '\n\033[1mfirst run ok\033[0m\n'
