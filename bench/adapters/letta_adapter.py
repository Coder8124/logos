#!/usr/bin/env python3
"""Letta (formerly MemGPT) under the continuity benchmark.

Speaks the bridge protocol on stdin/stdout: one JSON object per line in, one
per line out. See internal/eval/adapters/bridge.go.

What is measured, and why
-------------------------
Letta is an agent framework, not a memory library. Its memory has three parts:
core memory blocks that live in the context window, recall memory over the
message history, and *archival* memory — an embedded passage store the agent
searches on demand. Archival is the part that corresponds to what every other
system in this table does, so archival is what is scored: passages in,
semantic search out.

The alternative would be to run Letta's full agent loop, letting the model
decide what to write into core memory and when to search archival. That is
Letta at its most capable and it is not runnable here: one scenario writes
upwards of two hundred events, each becoming at least one local model call,
and the suite has thirty-two scenarios. The same call was made for mem0
(infer=False). In both cases the shortcut is *favourable* to the system under
test on a retrieval benchmark — nothing is lost to a small model's extraction —
and unfavourable on the reconciliation cases, which is stated in the write-up
rather than buried.

Running it requires more than an import: Letta 0.16 needs a PostgreSQL server
with pgvector and a running `letta server`. See bench/README.md. The probe below
fails cleanly when that is not up, so the row is dropped rather than reported
as zeros.
"""

import json
import os
import sys
import urllib.request

BASE = os.environ.get("LETTA_BASE_URL", "http://localhost:8289")
# The /v1 suffix is required: Letta routes every embedding through its
# OpenAI-compatible client and appends "/embeddings" to whatever endpoint it
# is given, regardless of embedding_endpoint_type. Ollama serves that path
# only under /v1.
OLLAMA = os.environ.get("LETTA_OLLAMA", "http://localhost:11434/v1")
EMBED_MODEL = os.environ.get("LETTA_EMBED", "nomic-embed-text")
EMBED_DIMS = int(os.environ.get("LETTA_EMBED_DIMS", "768"))
# The agent needs a model handle to be created at all, even though the agent
# loop is never run here. letta-free is the built-in default and costs nothing
# because no completion is ever requested.
MODEL = os.environ.get("LETTA_MODEL", "letta/letta-free")


def probe():
    """Exit non-zero with a readable reason if this system cannot run here."""
    try:
        from letta_client import Letta  # noqa: F401
    except Exception as e:
        print(f"letta_client not importable: {e}", file=sys.stderr)
        return 1
    try:
        with urllib.request.urlopen(f"{BASE}/v1/health/", timeout=5) as r:
            json.loads(r.read())
    except Exception:
        print(
            f"letta server unreachable at {BASE} — start it with "
            "`letta server --port 8289` (needs PostgreSQL + pgvector)",
            file=sys.stderr,
        )
        return 1
    return 0


class Adapter:
    def __init__(self):
        self.client = None
        self.agent_id = None

    def reset(self):
        from letta_client import Letta

        self.close()
        self.client = Letta(base_url=BASE)
        agent = self.client.agents.create(
            name=f"bench-{os.getpid()}-{id(self)}",
            model=MODEL,
            # Explicit config rather than the `ollama/...` handle: the handle
            # resolves to a provider row written at first server start, which
            # routes embeddings through the OpenAI-compatible path. Naming the
            # endpoint type here uses Ollama's native embeddings API instead.
            embedding_config={
                "embedding_endpoint_type": "ollama",
                "embedding_endpoint": OLLAMA,
                "embedding_model": EMBED_MODEL,
                "embedding_dim": EMBED_DIMS,
                "batch_size": 32,
            },
            # No memory blocks: core memory is filled by the agent loop, which
            # is not being run. Archival is the surface under test.
            memory_blocks=[],
            include_base_tools=False,
        )
        self.agent_id = agent.id

    def write(self, ev):
        # `flat` is the harness's prose rendering of an event — for a checkpoint
        # that means task, decisions, what failed and what is next, spelled out.
        # Letta has no checkpoint primitive, so this is the fullest form it can
        # take. created_at carries the event's real time, which the suite
        # backdates, so Letta's temporal filters see the same history brain does.
        kwargs = {"text": ev["flat"]}
        if ev.get("ts"):
            import datetime

            kwargs["created_at"] = datetime.datetime.fromtimestamp(
                ev["ts"], datetime.timezone.utc
            ).isoformat()
        self.client.agents.passages.create(self.agent_id, **kwargs)

    def read(self, q):
        query = q["task"]
        if q.get("project"):
            query = f"{q['project']}: {query}"
        hits = self.client.agents.passages.search(self.agent_id, query=query, top_k=20)

        results = getattr(hits, "results", None)
        if results is None:
            results = hits if isinstance(hits, list) else []

        budget = q.get("budget") or 2000
        out, used = [], 0
        for h in results:
            text = getattr(h, "content", None) or getattr(h, "text", None) or ""
            if not text and isinstance(h, dict):
                text = h.get("content") or h.get("text") or ""
            if not text:
                continue
            cost = len(text) // 4 + 1
            if used + cost > budget and out:
                break
            out.append(text)
            used += cost
        return "\n\n".join(out)

    def close(self):
        if self.client and self.agent_id:
            try:
                self.client.agents.delete(self.agent_id)
            except Exception:
                pass
        self.agent_id = None
        self.client = None


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
                reply = {"ok": False, "error": f"unknown op {op!r}"}
        except Exception as e:
            reply = {"ok": False, "error": f"{type(e).__name__}: {e}"}
        sys.stdout.write(json.dumps(reply) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
