#!/usr/bin/env python3
"""MemPalace under the continuity benchmark.

Speaks the bridge protocol on stdin/stdout. See internal/eval/adapters/bridge.go.

MemPalace is file-oriented: you put markdown in a directory, `mine` files it
into rooms and drawers, and `search` retrieves with cosine plus BM25. That maps
cleanly onto the suite — every event becomes a file, which is the shape the
system was designed for and its strongest form.

Isolation is per scenario via MEMPALACE_PALACE_PATH, so no run inherits another
run's drawers. Mining is run with --no-llm and the local embedding model, for
the same reason mem0 is run against Ollama: every system in the table gets the
same hardware and no network.

It is worth saying that MemPalace is the only third-party system here with a
handoff story of its own — it ships an `artifact` command for agent handoffs and
a `wake-up` context command. This benchmark does not use them: `artifact` is an
exact-file exchange rather than a retrieval surface, and wiring it would be
scoring a different feature than the one under test. The search path is what
answers "what do we know", and that is what is measured.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
MP = os.path.join(HERE, ".venv-mempalace", "bin", "mempalace")
EMBED_MODEL = os.environ.get("MEMPALACE_EMBEDDING_MODEL", "")


def probe():
    if not os.path.exists(MP):
        print(f"mempalace CLI not found at {MP}", file=sys.stderr)
        return 1
    try:
        import mempalace  # noqa: F401
    except Exception as e:
        print(f"mempalace not importable: {e}", file=sys.stderr)
        return 1
    return 0


class Adapter:
    def __init__(self):
        self.root = None
        self.palace = None
        self.n = 0
        self.mined = False

    def _env(self):
        env = dict(os.environ)
        env["MEMPALACE_PALACE_PATH"] = self.palace
        if EMBED_MODEL:
            env["MEMPALACE_EMBEDDING_MODEL"] = EMBED_MODEL
        return env

    def _run(self, args, timeout=900):
        # stdin must be closed, not inherited. `init` asks "Mine this directory
        # now? [Y/n]" even under --yes, and with the bridge's stdin attached it
        # blocks forever waiting for an answer that will never come.
        return subprocess.run(
            [MP] + args,
            env=self._env(),
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=timeout,
        )

    def reset(self):
        self.close()
        self.root = tempfile.mkdtemp(prefix="mempalace-src-")
        self.palace = tempfile.mkdtemp(prefix="mempalace-db-")
        os.makedirs(os.path.join(self.root, "notes"), exist_ok=True)
        self.n = 0
        self.mined = False

    def write(self, ev):
        # One file per event. Titled so the miner has something to key on, and
        # carrying the flattened prose so a checkpoint arrives complete.
        self.n += 1
        title = ev.get("title") or ev.get("task") or f"{ev['kind']} {self.n}"
        body = f"# {title}\n\n{ev['flat']}\n"
        name = f"{self.n:04d}-{ev['kind']}.md"
        with open(os.path.join(self.root, "notes", name), "w") as fh:
            fh.write(body)
        self.mined = False

    def _mine(self):
        init = self._run(["init", self.root, "--yes", "--no-llm"])
        if init.returncode != 0:
            raise RuntimeError(f"init failed: {init.stderr.strip()[-300:]}")
        mine = self._run(["mine", self.root])
        if mine.returncode != 0:
            raise RuntimeError(f"mine failed: {mine.stderr.strip()[-300:]}")
        self.mined = True

    def read(self, q):
        if not self.mined:
            self._mine()
        query = q["task"]
        if q.get("project"):
            query = f"{q['project']}: {query}"
        res = self._run(["search", query])
        if res.returncode != 0:
            raise RuntimeError(f"search failed: {res.stderr.strip()[-300:]}")

        # The CLI prints a banner, then indented result blocks. Keep the blocks.
        text = res.stdout
        budget = q.get("budget") or 2000
        if len(text) // 4 + 1 > budget:
            text = text[: budget * 4]
        return text

    def close(self):
        for path in (self.root, self.palace):
            if path:
                shutil.rmtree(path, ignore_errors=True)
        self.root = self.palace = None


def main():
    if "--probe" in sys.argv:
        sys.exit(probe())

    adapter = Adapter()
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            op = req.get("op")
            if op == "reset":
                adapter.reset()
                reply = {"ok": True}
            elif op == "write":
                adapter.write(req["event"])
                reply = {"ok": True}
            elif op == "read":
                reply = {"ok": True, "text": adapter.read(req["query"])}
            elif op == "close":
                adapter.close()
                reply = {"ok": True}
            else:
                reply = {"ok": False, "error": f"unknown op {op}"}
        except Exception as e:
            reply = {"ok": False, "error": f"{type(e).__name__}: {e}"}
        sys.stdout.write(json.dumps(reply) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
