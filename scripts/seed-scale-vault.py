#!/usr/bin/env python3
"""Seed a synthetic vault of a few million tokens, then check Logos against it.

Real vaults are a hundred notes; nothing here had ever been run against one the
size a busy team reaches after a year. This writes one: thousands of chained
checkpoints across several projects, in the exact shape `logos checkpoint`
writes, with known answers planted deep in the history. `--check` then asks
Logos for them and times each step, so "it still works at this size" is a
measurement rather than a hope.

    scripts/seed-scale-vault.py [vault-dir] [--tokens N] [--seed S]
    scripts/seed-scale-vault.py [vault-dir] --check path/to/logos

Destructive: wipes the target and re-seeds. It refuses anything that is not
empty or previously seeded by this script (marked by .logos/scale-seed), and
refuses ~/logos and ~/brain outright, since those are where real vaults live.

The text is generated, so it measures size and retrieval, not what a pack
saves: a bigger synthetic vault makes any "saved" figure bigger by
construction.
"""

import argparse
import json
import os
import random
import shutil
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone

MARKER = os.path.join(".logos", "scale-seed")
EXPECT = os.path.join(".logos", "scale-expect.json")

PROJECTS = ["atlas", "heron", "quarry", "lumen", "tessel", "orbit", "fathom", "kiln"]
AGENTS = ["claude-code", "codex", "cursor"]

# The vocabulary is wide on purpose: a vault of one repeated sentence would let
# ranking win on luck, and would compress into something no real vault looks like.
PARTS = ["the importer", "the retry queue", "the auth middleware", "the billing export",
         "the search indexer", "the webhook relay", "the migration runner", "the rate limiter",
         "the config loader", "the report builder", "the cache warmer", "the audit log",
         "the upload pipeline", "the session store", "the scheduler", "the PDF renderer",
         "the metrics exporter", "the feature flags", "the CSV parser", "the email digest"]
VERBS = ["rewrote", "split", "profiled", "instrumented", "moved", "simplified", "hardened",
         "batched", "parallelised", "documented", "retired", "replaced", "cached", "validated"]
WHY = ["p99 latency doubled under load", "a flaky test hid the real failure",
       "the fixture drifted from production", "memory grew without bound overnight",
       "two writers raced on the same row", "the upstream API paginates differently",
       "timezone handling broke at midnight UTC", "the lock was held across a network call",
       "a retry storm followed every deploy", "the error was swallowed and logged as success",
       "the build cache served a stale artifact", "Unicode input broke the tokenizer",
       "it passed locally and failed on CI's older runtime", "the queue reordered messages"]
FILES = ["api/handlers.go", "internal/queue/worker.go", "cmd/server/main.go", "pkg/auth/token.go",
         "internal/export/csv.go", "web/src/App.tsx", "internal/store/sql.go", "Makefile",
         "internal/sched/cron.go", "deploy/k8s/app.yaml", "internal/render/pdf.go", "go.mod"]
CMDS = ["go test ./...", "make lint", "npm test", "go test -race ./internal/...",
        "pytest -q", "make e2e", "go vet ./...", "docker compose up --build"]

# The planted answers. Each sits far back in a long history, where a pack that
# only looked at recent checkpoints, or ranked badly, would miss it.
NEEDLE_PROJECT = "atlas"
NEEDLE_AT = 12
NEEDLE_ROUTE = "shard the ledger table by tenant id"
NEEDLE_WHY = "one hot tenant pinned a single shard at 100% CPU while the others idled"


def sentence(r):
    return f"{r.choice(VERBS).capitalize()} {r.choice(PARTS)} because {r.choice(WHY)}."


def checkpoint(r, project, n, sid, prev, when, agent, extra_failed):
    task = f"{r.choice(VERBS).capitalize()} {r.choice(PARTS)} and {r.choice(PARTS)} ({project} #{n})"
    touched = r.sample(FILES, 3)
    rel = [f'  - {{ pred: checkpoint_of, obj: "[[{project}]]", conf: 1.0, src: stated }}']
    if prev:
        rel.append(f'  - {{ pred: follows, obj: "[[{prev}]]", conf: 1.0, src: stated }}')
    failed = [f"{r.choice(VERBS).capitalize()} {r.choice(PARTS)}: {r.choice(WHY)}"
              for _ in range(r.randint(2, 4))] + extra_failed
    out = ["---", "type: checkpoint", f"title: '{project} — {task[:70]}'", f"project: {project}",
           f"agent: {agent}", f"session: {sid}", f"branch: work-{n}", f"commit: {r.getrandbits(28):07x}",
           "uncommitted: 0", "touched:"] + [f"  - {t}" for t in touched] + ["relations:"] + rel + [
           f"first_seen: {when:%Y-%m-%d}", f"checkpointed: {when.isoformat()}", "---", "",
           "## Task", "", task, "", "## State", "",
           " ".join(sentence(r) for _ in range(r.randint(6, 10))), "", "## Decisions", ""]
    out += [f"- {sentence(r)}" for _ in range(r.randint(3, 5))]
    out += ["", "## Didn't work", ""] + [f"- {f}" for f in failed]
    out += ["", "## Verified", ""]
    out += [f"- {sentence(r)[:-1]}: {r.choice(CMDS)}" for _ in range(r.randint(3, 5))]
    out += ["", "## Commands run", ""] + [f"- {c}" for c in r.sample(CMDS, 3)]
    out += ["", "## Open questions", "", f"- Does {r.choice(PARTS)} still need {r.choice(PARTS)}?"]
    out += ["", "## Next", "", f"{r.choice(VERBS).capitalize()} {r.choice(PARTS)} next.", ""]
    return task, "\n".join(out)


def refuse(msg):
    print(f"refusing: {msg}", file=sys.stderr)
    sys.exit(1)


def seed(vault, tokens, seed_n):
    home = os.path.expanduser("~")
    if os.path.realpath(vault) in {os.path.realpath(os.path.join(home, d)) for d in ("logos", "brain")}:
        refuse(f"{vault} is where a real vault lives.")
    if os.path.exists(vault) and os.listdir(vault) and not os.path.exists(os.path.join(vault, MARKER)):
        refuse(f"{vault} is non-empty and wasn't seeded by this script.")
    shutil.rmtree(vault, ignore_errors=True)
    os.makedirs(os.path.join(vault, ".logos"))
    open(os.path.join(vault, MARKER), "w").close()

    r = random.Random(seed_n)
    start = datetime(2025, 1, 6, 9, 0, tzinfo=timezone(timedelta(hours=-7)))
    state = {p: {"n": 0, "prev": None, "when": start + timedelta(minutes=37 * i), "task": None}
             for i, p in enumerate(PROJECTS)}
    written = files = 0
    while written / 4 < tokens:
        for p in PROJECTS:
            s = state[p]
            s["n"] += 1
            s["when"] += timedelta(hours=r.randint(3, 30), minutes=r.randint(0, 59))
            agent = r.choice(AGENTS)
            sid = f"{s['when']:%Y%m%d-%H%M%S}-{agent}"
            extra = [f"{NEEDLE_ROUTE}: {NEEDLE_WHY}"] if p == NEEDLE_PROJECT and s["n"] == NEEDLE_AT else []
            task, body = checkpoint(r, p, s["n"], sid, s["prev"], s["when"], agent, extra)
            d = os.path.join(vault, "sessions", p)
            os.makedirs(d, exist_ok=True)
            with open(os.path.join(d, sid + ".md"), "w") as f:
                f.write(body)
            written += len(body.encode())
            files += 1
            s["prev"], s["task"] = sid, task

    expect = {"latest_task": {p: state[p]["task"] for p in PROJECTS},
              "checkpoints": {p: state[p]["n"] for p in PROJECTS},
              "needle": {"project": NEEDLE_PROJECT, "at": NEEDLE_AT, "route": NEEDLE_ROUTE, "why": NEEDLE_WHY},
              "bytes": written, "files": files}
    with open(os.path.join(vault, EXPECT), "w") as f:
        json.dump(expect, f, indent=2)
    print(f"seeded {vault}: {files:,} checkpoints across {len(PROJECTS)} projects, "
          f"{written:,} bytes (~{written // 4:,} tokens at 4 characters a token)")


def run(logos, vault, *args):
    env = dict(os.environ, LOGOS_VAULT=vault, LOGOS_USAGE="off")
    t = time.monotonic()
    p = subprocess.run([logos, *args], env=env, capture_output=True, text=True)
    return p, time.monotonic() - t


def check(vault, logos):
    if not os.path.exists(os.path.join(vault, MARKER)):
        refuse(f"{vault} wasn't seeded by this script.")
    exp = json.load(open(os.path.join(vault, EXPECT)))
    ok = True

    def report(passed, what):
        nonlocal ok
        ok &= passed
        print(f"{'PASS' if passed else 'FAIL'}  {what}")

    p, dt = run(logos, vault, "index")
    report(p.returncode == 0, f"logos index over {exp['files']:,} checkpoints: {dt:.1f}s"
           + ("" if p.returncode == 0 else f"\n{p.stderr[-2000:]}"))

    for proj in PROJECTS:
        p, dt = run(logos, vault, "resume", proj)
        size = next((l for l in p.stdout.splitlines() if "Actual size" in l), "no size line")
        want = exp["latest_task"][proj]
        report(p.returncode == 0 and want in p.stdout,
               f"resume {proj} ({exp['checkpoints'][proj]} checkpoints) names the latest task, "
               f"{dt * 1000:.0f} ms, {size.strip('_ ')}")

    n = exp["needle"]
    p, dt = run(logos, vault, "tried", "shard the ledger by tenant", "--project", n["project"])
    report(p.returncode == 0 and "hot tenant" in p.stdout,
           f"tried finds the dead end from checkpoint {n['at']} of {exp['checkpoints'][n['project']]} "
           f"in {n['project']}, {dt * 1000:.0f} ms")
    if "hot tenant" not in p.stdout:
        print(p.stdout[-1500:] + p.stderr[-500:])

    p, dt = run(logos, vault, "tried", "shard the ledger by tenant")
    report(p.returncode == 0 and "hot tenant" in p.stdout,
           f"tried without --project still finds it, {dt * 1000:.0f} ms")

    p, dt = run(logos, vault, "tried", "move the email digest onto a cron in another region", "--project", "atlas")
    report(p.returncode == 0 and "hot tenant" not in p.stdout,
           f"an unrelated approach is not matched to the planted dead end, {dt * 1000:.0f} ms")
    sys.exit(0 if ok else 1)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("vault", nargs="?", default=os.path.expanduser("~/vaults/scale"))
    ap.add_argument("--tokens", type=int, default=2_000_000)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--check", metavar="LOGOS")
    a = ap.parse_args()
    if a.check:
        check(a.vault, a.check)
    else:
        seed(a.vault, a.tokens, a.seed)


if __name__ == "__main__":
    main()
