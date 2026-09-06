#!/usr/bin/env python3
"""Probe `brain mcp serve` over stdio.

A shell pipeline into the server hangs: the server keeps stdin open waiting for
the next JSON-RPC frame, and `echo ... | brain mcp serve` never closes it in a
way that produces readable output. This script does the newline-framed exchange
the transport actually wants — one request, one response, in order — so you can
see the server work without wiring a real host.

    scripts/mcp-probe.py                       # initialize + tools/list
    scripts/mcp-probe.py resume brain          # also call the `resume` tool
    BRAIN_VAULT=$(mktemp -d) scripts/mcp-probe.py

With a tool name and optional JSON args:

    scripts/mcp-probe.py recall '{"query": "the vault lock"}'
"""

import json
import os
import subprocess
import sys


def main() -> int:
    args = sys.argv[1:]
    tool = args[0] if args else None
    tool_args: dict = {}
    if len(args) == 2:
        try:
            tool_args = json.loads(args[1])
        except json.JSONDecodeError:
            # A bare word after the tool name is the common case: `resume brain`.
            tool_args = {"project": args[1]}
    elif len(args) > 2:
        tool_args = {"project": args[1]}

    # Prefer a build next to this checkout over whatever is on PATH, so the
    # probe tests the code you are editing rather than the wired release.
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    local_bin = os.path.join(root, "bin", "brain")
    brain = local_bin if os.path.exists(local_bin) else "brain"

    proc = subprocess.Popen(
        [brain, "mcp", "serve"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=None,  # straight to our stderr — that is where the server talks
        text=True,
    )

    def roundtrip(method: str, params: dict, req_id: int) -> dict:
        proc.stdin.write(json.dumps(
            {"jsonrpc": "2.0", "id": req_id, "method": method, "params": params}
        ) + "\n")
        proc.stdin.flush()
        line = proc.stdout.readline()
        if not line:
            raise SystemExit("server closed the stream with no response")
        return json.loads(line)

    def notify(method: str, params: dict) -> None:
        proc.stdin.write(json.dumps(
            {"jsonrpc": "2.0", "method": method, "params": params}
        ) + "\n")
        proc.stdin.flush()

    try:
        init = roundtrip("initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "mcp-probe", "version": "0"},
        }, 1)
        print("initialize →", json.dumps(init, indent=2))

        notify("notifications/initialized", {})

        listed = roundtrip("tools/list", {}, 2)
        names = [t["name"] for t in listed.get("result", {}).get("tools", [])]
        print(f"\ntools/list → {len(names)} tools: {', '.join(names)}")

        if tool:
            called = roundtrip("tools/call", {"name": tool, "arguments": tool_args}, 3)
            print(f"\ntools/call {tool} →", json.dumps(called, indent=2))
    finally:
        proc.stdin.close()
        proc.wait(timeout=5)

    return 0


if __name__ == "__main__":
    sys.exit(main())
