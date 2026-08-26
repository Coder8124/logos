#!/usr/bin/env python3
"""mem0 under the continuity benchmark.

Speaks the bridge protocol on stdin/stdout: one JSON object per line in, one
per line out. See internal/eval/adapters/bridge.go.

Everything runs against Ollama, so no API key is needed and nothing leaves the
machine — the same local models brain is scored with.

One deviation worth stating plainly. mem0's `add(infer=True)` runs an LLM over
every write to extract and reconcile facts, which is its real design and its
real advantage on contradictions. It is also one model call per event, and the
suite writes over two hundred events in a single scenario. The default here is
infer=False, which stores text verbatim and embeds it. That is faster and, for
a retrieval benchmark, mostly *favourable* to mem0: nothing is lost to
extraction. Set MEM0_INFER=1 to run it the slow, authentic way; the write-up
reports both where it was affordable to.
"""

import json
import os
import shutil
import sys
import tempfile

# mem0 ships analytics on by default and opens a PostHog client at import. A
# benchmark that claims every system runs locally has to actually mean it, so
# this is set before mem0 is imported anywhere below.
os.environ.setdefault("MEM0_TELEMETRY", "False")
os.environ.setdefault("ANONYMIZED_TELEMETRY", "False")

OLLAMA = os.environ.get("OLLAMA_HOST", "http://localhost:11434")
LLM_MODEL = os.environ.get("MEM0_LLM", "gemma3:4b")
EMBED_MODEL = os.environ.get("MEM0_EMBED", "nomic-embed-text")
EMBED_DIMS = int(os.environ.get("MEM0_EMBED_DIMS", "768"))
INFER = os.environ.get("MEM0_INFER", "") == "1"
USER = "bench"


def probe():
    """Exit non-zero with a readable reason if this system cannot run here."""
    try:
        import ollama  # noqa: F401
        from mem0 import Memory  # noqa: F401
    except Exception as e:  # pragma: no cover - diagnostic path
        print(f"mem0 not importable: {e}", file=sys.stderr)
        return 1
    try:
        import urllib.request

        urllib.request.urlopen(f"{OLLAMA}/api/tags", timeout=5).read()
    except Exception:
        print(f"ollama unreachable at {OLLAMA}", file=sys.stderr)
        return 1
    return 0


class Adapter:
    def __init__(self):
        self.mem = None
        self.dir = None

    def reset(self):
        from mem0 import Memory

        self.close()
        self.dir = tempfile.mkdtemp(prefix="mem0-bench-")
        self.mem = Memory.from_config(
            {
                "llm": {
                    "provider": "ollama",
                    "config": {"model": LLM_MODEL, "ollama_base_url": OLLAMA},
                },
                "embedder": {
                    "provider": "ollama",
                    "config": {"model": EMBED_MODEL, "ollama_base_url": OLLAMA},
                },
                "vector_store": {
                    "provider": "qdrant",
                    "config": {
                        "path": self.dir,
                        "collection_name": "bench",
                        "embedding_model_dims": EMBED_DIMS,
                    },
                },
            }
        )

    def write(self, ev):
        # `flat` is the harness's prose rendering, which spells a checkpoint out
        # in full — task, decisions, what failed, what is next. mem0 has no
        # checkpoint primitive, so this is the most complete form it can accept.
        self.mem.add(ev["flat"], user_id=USER, infer=INFER)

    def read(self, q):
        query = q["task"]
        if q.get("project"):
            query = f"{q['project']}: {query}"
        hits = self.mem.search(query, filters={"user_id": USER}, top_k=20)
        results = hits.get("results", hits) if isinstance(hits, dict) else hits

        budget = q.get("budget") or 2000
        out, used = [], 0
        for h in results:
            text = h.get("memory") or h.get("text") or ""
            cost = len(text) // 4 + 1
            if used + cost > budget and out:
                break
            out.append(text)
            used += cost
        return "\n\n".join(out)

    def close(self):
        self.mem = None
        if self.dir:
            shutil.rmtree(self.dir, ignore_errors=True)
            self.dir = None


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
