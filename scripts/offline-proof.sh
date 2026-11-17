#!/usr/bin/env bash
#
# Run logos's everyday commands with the network gone, to prove invariant 5
# ("nothing leaves the machine") by running it rather than by asserting it.
#
#   scripts/offline-proof.sh                       # builds, then runs (not offline: see below)
#   LOGOS_BIN=./logos unshare -rn scripts/offline-proof.sh   # Linux: really offline
#   OFFLINE_REQUIRE=1 ...                          # fail unless the network is verifiably gone
#   TRACE_CONNECT=1 ...                            # strace every connect(); fail on one past loopback or to DNS
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
# Exit codes alone are not the proof: a command that tries the network, fails,
# and carries on (an update check, a runtime probe) exits 0 offline too. With
# TRACE_CONNECT every logos process runs under strace, and any connect() to an
# address other than loopback, or to port 53 anywhere, fails the run, whether
# or not the command noticed. Loopback is the user's local model runtime; unix sockets are IPC.
#
# Build the binary before entering the namespace (LOGOS_BIN): the toolchain and
# its module cache are not part of what is being proved.
set -uo pipefail

if [ -n "${LOGOS_BIN:-}" ]; then
	LOGOS_BIN=$(cd "$(dirname "$LOGOS_BIN")" && pwd)/$(basename "$LOGOS_BIN") || exit 1
fi
cd "$(dirname "$0")/.." || exit 1

# A connection attempt that succeeds, or fails any way but "network
# unreachable", means the namespace is not doing its job. It also catches a
# runner whose unshare quietly did nothing.
if [ -n "${OFFLINE_REQUIRE:-}" ]; then
	if python3 -I - <<'PY'
import errno, socket, sys
# Only ENETUNREACH is proof. ECONNREFUSED on loopback is what every ordinary
# machine with no model runtime says, and a timeout is a slow or filtered
# network: both passed this check once and would have green-lit a runner whose
# unshare did nothing.
for host, port in (("1.1.1.1", 53), ("127.0.0.1", 11434)):
    s = socket.socket()
    s.settimeout(2)
    try:
        s.connect((host, port))
    except OSError as e:
        if e.errno == errno.ENETUNREACH:
            continue
        print("%s:%d failed with %s, not 'network unreachable': this shell is not proven offline" % (host, port, e))
        sys.exit(1)
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
traced=0
if [ -n "${TRACE_CONNECT:-}" ] && ! command -v strace >/dev/null; then
	echo "TRACE_CONNECT is set and strace is not installed; refusing to claim a proof"
	exit 1
fi

# Every command must exit 0: a command that tried the network and gave up would
# say so by failing, and a success here is the claim. LOGOS_RUNTIME is left to
# the default on purpose, so the runtime probe that would hit localhost runs
# and is shown to fail soft rather than being switched off.
run() {
	local what=$1
	shift
	printf '\n\033[1m$ logos %s\033[0m\n' "$what"
	local trace=()
	if [ -n "${TRACE_CONNECT:-}" ]; then
		traced=$((traced + 1))
		trace=(strace -f -qq -e trace=connect -o "$WORK/connect.$traced")
	fi
	# The ${a+...} form: bash 3.2 (macOS) treats an empty array as unset under -u.
	env -i HOME="$HOME_DIR" XDG_CONFIG_HOME="$HOME_DIR/.config" LOGOS_VAULT="$VAULT" \
		PATH="$HOME_DIR/.path:/usr/bin:/bin" TERM="${TERM:-dumb}" ${trace[@]+"${trace[@]}"} "$BIN" "$@"
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
	PATH="$HOME_DIR/.path:/usr/bin:/bin" BIN="$BIN" \
	TRACE_OUT="${TRACE_CONNECT:+$WORK/connect.mcp}" python3 -I - <<'PY'
import json, os, subprocess, sys

# A pipeline into `mcp serve` hangs, so this writes one line and reads one back.
argv = [os.environ["BIN"], "mcp", "serve"]
if os.environ.get("TRACE_OUT"):
    argv = ["strace", "-f", "-qq", "-e", "trace=connect", "-o", os.environ["TRACE_OUT"]] + argv
proc = subprocess.Popen(argv, stdin=subprocess.PIPE,
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

if [ -n "${TRACE_CONNECT:-}" ]; then
	printf '\n\033[1m$ connect() calls past loopback, or to DNS\033[0m\n'
	all=$(cat "$WORK"/connect.* 2>/dev/null | grep 'connect(' || true)
	# Port 53 counts wherever it goes: a lookup by hostname connects to the
	# resolver on loopback (127.0.0.53, or Go's 127.0.0.1:53 fallback) and fails
	# there, before any connect() past loopback is made, so a fail-soft call to
	# a named host would otherwise leave no outward trace at all.
	outward=$(printf '%s\n' "$all" |
		awk '/AF_INET/ && (/htons\(53\)/ || !/inet_addr\("127\.|"::1"|"::ffff:127\./)' || true)
	printf '%d connect() calls traced, %d past loopback or to DNS\n' \
		"$(printf '%s' "$all" | grep -c 'connect(' || true)" \
		"$(printf '%s' "$outward" | grep -c 'connect(' || true)"
	if [ -n "$outward" ]; then
		printf '%s\n' "$outward"
		failures+=("a logos process tried to connect past loopback or to DNS (above)")
	fi
fi

if [ ${#failures[@]} -gt 0 ]; then
	printf '\n\033[1;31moffline proof failed:\033[0m\n'
	printf '  %s\n' "${failures[@]}"
	exit 1
fi
printf '\n\033[1moffline proof ok\033[0m\n'
