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

Two modes, and the difference is the whole caveat
-------------------------------------------------
**Default (`LETTA_AGENT_LOOP` unset).** Passages go straight into archival and
come back by semantic search. No completion is ever requested, so the run is
fast and deterministic. On a retrieval benchmark this is *favourable* to Letta —
nothing is lost to a small model's extraction — and unfavourable on the
reconciliation cases, where deciding that a new number replaces an old one is
exactly the work the loop does and this mode skips.

**`LETTA_AGENT_LOOP=1`.** Every write becomes a real agent turn: the event is
sent as a message, and the model decides what to put in core memory and what to
file in archival, with the base memory tools available to it. That is Letta as
its authors intend it, and it is the mode that answers the caveat above.

Reads are deliberately identical in both modes: archival search, plus whatever
the loop wrote into core memory. The benchmark scores *the context a system
hands the next agent*, not a generated answer, so asking the agent a question
and grading its reply would be measuring something no other row measures. Core
memory is included because in agent-loop mode that is where the loop puts what
it considers settled — leaving it out would run the loop and then discard its
main output.

Cost, measured rather than guessed: the suite is 596 write events across 32
scenarios (201 of them in `scale-haystack` alone). A write is not one model
call — timing `supersession-current-value` gave 108 chat completions for 23
writes, about 4.7 each, at ~33 s per event, because a turn is reason → call a
tool → read the result → often step again. That puts the suite near 5.4 hours,
and superlinear rather than flat: context grows as memory accumulates, so long
scenarios cost more per write than short ones.

What the loop buys, on that scenario: fidelity 0% → 50%. Archival mode returns
all three superseded prices, exactly as plain vector search does; the loop
drops the first entirely and leaks only the second. The scenario still fails,
because `pass` is conjunctive and one leak scores like ten — but the difference
is real, and it is only visible in `fidelity`.

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
AGENT_LOOP = os.environ.get("LETTA_AGENT_LOOP", "") == "1"

# In archival-only mode the agent still needs a model handle to be created at
# all, even though no completion is ever requested; letta-free is the built-in
# default and costs nothing because the loop never runs.
#
# In agent-loop mode the model actually runs, so it has to be local — a
# benchmark that claims every system stays on the machine cannot quietly send
# 596 events to a hosted endpoint. The handle must be one `letta server` has
# discovered; ask it with `client.models.list()`. Note that Letta filters
# Ollama models by tool-calling support, so small models may not appear.
DEFAULT_MODEL = "ollama/glm-4.7-flash:latest" if AGENT_LOOP else "letta/letta-free"
MODEL = os.environ.get("LETTA_MODEL", DEFAULT_MODEL)


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
            # Archival-only: core memory is filled by the agent loop, which is
            # not being run, so there is nothing to give it. Agent-loop mode
            # needs a block to write into and the base tools to write with.
            memory_blocks=(
                [
                    {
                        "label": "project",
                        "value": "",
                        "description": (
                            "What is currently true about the work: decisions and "
                            "the reasons for them, what has been ruled out and why, "
                            "open questions, and the next step. Replace a fact when "
                            "it is superseded rather than appending beside it."
                        ),
                        "limit": 4000,
                    }
                ]
                if AGENT_LOOP
                else []
            ),
            include_base_tools=AGENT_LOOP,
        )
        self.agent_id = agent.id

    def write(self, ev):
        # `flat` is the harness's prose rendering of an event — for a checkpoint
        # that means task, decisions, what failed and what is next, spelled out.
        # Letta has no checkpoint primitive, so this is the fullest form it can
        # take.
        if AGENT_LOOP:
            return self._write_through_loop(ev)

        # created_at carries the event's real time, which the suite backdates,
        # so Letta's temporal filters see the same history brain does.
        kwargs = {"text": ev["flat"]}
        if ev.get("ts"):
            kwargs["created_at"] = self._iso(ev["ts"])
        self.client.agents.passages.create(self.agent_id, **kwargs)

    def _write_through_loop(self, ev):
        """One agent turn per event: the model decides what to keep and where.

        The message says when the event happened because the loop cannot see
        `created_at` on a passage it has not written yet, and half the suite
        turns on which of two facts is later. It does not tell the agent *how*
        to store anything beyond that — choosing between core and archival, and
        deciding that a new number replaces an old one, is the work under test.
        """
        when = f" (recorded {self._iso(ev['ts'])})" if ev.get("ts") else ""
        self.client.agents.messages.create(
            self.agent_id,
            messages=[
                {
                    "role": "user",
                    "content": f"Record this{when}:\n\n{ev['flat']}",
                }
            ],
        )

    @staticmethod
    def _iso(ts):
        import datetime

        return datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).isoformat()

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

        # Core memory first, and only in agent-loop mode, where it holds what
        # the loop decided was settled. It is what Letta would actually put in
        # the next agent's context window, so leaving it out would run the loop
        # and then throw away its main output. It goes first because it is what
        # Letta itself ranked as most important, and the budget is tight.
        if AGENT_LOOP:
            for text in self._core_memory():
                cost = len(text) // 4 + 1
                if used + cost > budget and out:
                    break
                out.append(text)
                used += cost

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

    def _core_memory(self):
        """The agent's core memory blocks, as plain text. Empty on any failure —
        a read that raises would score as a crash rather than as a miss."""
        try:
            blocks = self.client.agents.blocks.list(self.agent_id)
        except Exception:
            return []
        texts = []
        for b in blocks:
            value = getattr(b, "value", None) or ""
            if value.strip():
                texts.append(value.strip())
        return texts

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
