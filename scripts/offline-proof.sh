#!/usr/bin/env bash
#
# Run logos's everyday commands with the network gone, to prove invariant 5
# ("nothing leaves the machine") by running it rather than by asserting it.
#
#   scripts/offline-proof.sh                       # builds, then runs (not offline: see below)
#   LOGOS_BIN=./logos unshare -rn scripts/offline-proof.sh   # Linux: really offline
#   OFFLINE_REQUIRE=1 ...                          # fail unless the network is verifiably gone
#
# On its own the script only checks that the sequence works against a scratch
# vault and a scratch HOME; it proves nothing about the network. The proof is
# running it inside `unshare -rn`, a network namespace with no interfaces up —
# not even loopback — so every connection attempt, to the internet or to a
# local model runtime, fails at once. A command that needed the network then
# fails the job, where a test that stubs HTTP would have let it through. CI
# sets OFFLINE_REQUIRE=1 so that a runner which silently refused the namespace
# cannot turn this into a green check that proved nothing.
#
# Everything lives under a directory made here, never ~/logos or ~/brain, and
# env -i keeps an inherited LOGOS_VAULT from pointing the walk at a real vault.
# Like first-run.sh the scratch directory is inside the checkout: setup refuses
# to record a vault under a temporary directory.
#
# Build the binary before entering the namespace (LOGOS_BIN): the toolchain and
# its module cache are not part of what is being proved.
set -uo pipefail

if [ -n "${LOGOS_BIN:-}" ]; then
	LOGOS_BIN=$(cd "$(dirname "$LOGOS_BIN")" && pwd)/$(basename "$LOGOS_BIN") || exit 1
fi
cd "$(dirname "$0")/.." || exit 1

# A connection attempt that succeeds means the namespace is not doing its job.
# It also catches a runner whose unshare quietly did nothing.
if [ -n "${OFFLINE_REQUIRE:-}" ]; then
	if python3 -I - <<'PY'
import socket, sys
for host in ("1.1.1.1", "127.0.0.1"):
    s = socket.socket()
    s.settimeout(2)
    try:
        s.connect((host, 53 if host != "127.0.0.1" else 11434))
    except OSError:
        continue
    print("connected to %s: this shell is not offline" % host)
    sys.exit(1)
PY
	then
		echo "network is verifiably unreachable"
	else
		echo "OFFLINE_REQUIRE is set and the network is reachable; refusing to claim a proof"
		exit 1
	fi
fi

mkdir -p .offline-proof || exit 1
WORK=$(mktemp -d "$PWD/.offline-proof/run.XXXXXX") || exit 1
trap 'rm -rf "$PWD/.offline-proof"' EXIT
HOME_DIR=$WORK/home
VAULT=$WORK/vault
mkdir -p "$HOME_DIR/.path" "$HOME_DIR/.config" "$HOME_DIR/.cursor" "$HOME_DIR/.config/Claude" "$HOME_DIR/Library/Application Support/Claude"

if [ -n "${LOGOS_BIN:-}" ]; then
	BIN=$LOGOS_BIN
else
	BIN=$WORK/logos
	go build -o "$BIN" ./cmd/logos || exit 1
fi

failures=()

# Every command must exit 0: a command that tried the network and gave up would
# say so by failing, and a success here is the claim. LOGOS_RUNTIME is left to
# the default on purpose, so the runtime probe that would hit localhost runs
# and is shown to fail soft rather than being switched off.
run() {
	local what=$1
	shift
	printf '\n\033[1m$ logos %s\033[0m\n' "$what"
	env -i HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" LOGOS_VAULT="$VAULT" \
		PATH="$HOME_DIR/.path:/usr/bin:/bin" TERM="${TERM:-dumb}" "$BIN" "$@"
	local got=$?
	printf '\033[2m[exit %d]\033[0m\n' $got
	[ $got -eq 0 ] || failures+=("logos $what exited $got, want 0")
}

run "setup --yes" setup --yes
run "checkpoint" checkpoint offline-proof --task "prove nothing leaves" --next "read the job log" --verified "ran with no network"
run "resume" resume offline-proof
run "index" index
run "import --from transcripts --yes (harvest)" import --from transcripts --yes
run "memory recall" memory recall "prove nothing leaves"

printf '\n\033[1m$ MCP initialize + tools/list\033[0m\n'
if ! env -i HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" LOGOS_VAULT="$VAULT" \
	PATH="$HOME_DIR/.path:/usr/bin:/bin" BIN="$BIN" python3 -I - <<'PY'
import json, os, subprocess, sys

# A pipeline into `mcp serve` hangs, so this writes one line and reads one back.
proc = subprocess.Popen([os.environ["BIN"], "mcp", "serve"], stdin=subprocess.PIPE,
                        stdout=subprocess.PIPE, text=True)

def ask(method, params, id=None):
    msg = {"jsonrpc": "2.0", "method": method, "params": params}
    if id is not None:
        msg["id"] = id
    proc.stdin.write(json.dumps(msg) + "\n")
    proc.stdin.flush()
    if id is None:
        return None
    line = proc.stdout.readline()
    if not line:
        sys.exit("server closed the stream with no response to " + method)
    return json.loads(line)

try:
    init = ask("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                              "clientInfo": {"name": "offline-proof", "version": "0"}}, 1)
    if "result" not in init:
        sys.exit("initialize failed: %s" % init)
    ask("notifications/initialized", {})
    tools = ask("tools/list", {}, 2).get("result", {}).get("tools", [])
    if not tools:
        sys.exit("tools/list returned no tools")
    print("initialize ok; tools/list: %d tools" % len(tools))
finally:
    proc.stdin.close()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
PY
then
	failures+=("the MCP stdio round trip failed")
fi

if [ ${#failures[@]} -gt 0 ]; then
	printf '\n\033[1;31moffline proof failed:\033[0m\n'
	printf '  %s\n' "${failures[@]}"
	exit 1
fi
printf '\n\033[1moffline proof ok\033[0m\n'
