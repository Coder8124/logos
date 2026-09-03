// Frontend logic. Deliberately dependency-free and keyboard-first: the panel is
// invoked dozens of times a day and must open instantly and be operable without
// the mouse. All state lives in the Go backend; this only renders and dispatches.

const go = () => window.go?.main?.App;
const $ = (id) => document.getElementById(id);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
};

function escapeHtml(s) {
  return (s || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// relTime turns a unix-seconds timestamp into "3m ago" / "never" — every state
// signal in this app is shown as an age, not a raw number, so it reads as proof
// of life rather than a static label.
function relTime(unixSec) {
  if (!unixSec) return "never";
  const diff = Math.max(0, Math.floor(Date.now() / 1000) - unixSec);
  if (diff < 5) return "just now";
  if (diff < 60) return diff + "s ago";
  if (diff < 3600) return Math.floor(diff / 60) + "m ago";
  if (diff < 86400) return Math.floor(diff / 3600) + "h ago";
  return Math.floor(diff / 86400) + "d ago";
}

let current = "sessions";

// ---- orb / status ----

async function refreshStatus() {
  try {
    const s = await go().Status();
    const orb = $("orb");
    orb.className = "orb idle";

    const badge = $("pending-badge");
    const waiting = s.pending || 0;
    badge.textContent = waiting;
    badge.hidden = waiting === 0;
  } catch (_) {
    // Backend not up yet during dev; leave the UI in its resting state.
  }
}

function thinking(on) {
  const orb = $("orb");
  if (on) orb.className = "orb thinking";
  else refreshStatus();
}

// ---- state strip: the product's "it's actually working" proof ----
// Every count here is read straight off the vault or the index, never held in
// JS state, so the strip cannot drift from what the backend actually holds.

function statRow(bar, k, v, cls) {
  const s = el("span", "stat" + (cls ? " " + cls : ""));
  s.append(el("span", "k", k), el("span", "v", String(v)));
  bar.append(s);
}

async function refreshOverview() {
  try {
    const o = await go().Overview();
    const bar = $("statebar");
    bar.innerHTML = "";
    statRow(bar, "NOTES", o.notes);
    statRow(bar, "MEM", o.memories);
    statRow(bar, "CHECKPOINTS", o.checkpoints);
    statRow(bar, "OPEN", o.openSessions, o.openSessions > 0 ? "warn" : "");
    statRow(bar, "PROJECTS", o.projects);
    statRow(bar, "INDEX", relTime(o.indexBuilt));
    statRow(bar, "VAULT", relTime(o.vaultWritten));
    statRow(bar, "CAPTURE", o.recording ? "● live" : "○ idle", o.recording ? "rec" : "idle-rec");
    const asof = el("span", "asof", "as of " + new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }));
    bar.append(asof);

    $("vault-path").textContent = o.vault || "";
    $("vault-path").title = o.vault || "";
    $("orb").title = o.recording ? "capturing" : "idle — not capturing";
  } catch (_) {
    // Leave the last-known strip up rather than blanking it on a transient error.
  }
}

// ---- sessions: checkpoints, browsable newest first ----

function pill(cls, text) { return el("span", "pill " + cls, text); }

function renderSessionRow(c, active) {
  const row = el("button", "split-row" + (active ? " active" : ""));
  row.type = "button";
  row.append(el("div", "proj", c.project || "—"));
  row.append(el("div", "task", c.task || "(no task recorded)"));
  const meta = el("div", "meta");
  meta.append(el("span", "agent", c.agent || "agent"));
  if (c.verified && c.verified.length) meta.append(pill("good", "✓" + c.verified.length));
  if (c.blockers && c.blockers.length) meta.append(pill("bad", "✕" + c.blockers.length));
  meta.append(document.createTextNode(relTime(c.ts)));
  row.append(meta);
  row.addEventListener("click", () => {
    document.querySelectorAll("#sessions-list .split-row").forEach((r) => r.classList.remove("active"));
    row.classList.add("active");
    renderCheckpointDetail(c);
  });
  return row;
}

function cpBlock(kindCls, title, text) {
  const b = el("div", "cp-block" + (kindCls ? " " + kindCls : ""));
  if (title) b.append(el("h4", null, title));
  b.append(el("div", "cp-state", text));
  return b;
}

function cpList(kindCls, title, items, mono) {
  const b = el("div", "cp-block" + (kindCls ? " " + kindCls : ""));
  b.append(el("h4", null, title));
  const ul = el("ul");
  items.forEach((i) => {
    const li = document.createElement("li");
    if (mono) li.append(el("code", null, i));
    else li.textContent = i;
    ul.append(li);
  });
  b.append(ul);
  return b;
}

function renderCheckpointDetail(c) {
  const box = $("sessions-detail");
  box.innerHTML = "";

  const head = el("div", "cp-head");
  head.append(el("div", "cp-title", c.task || "(untitled checkpoint)"));
  const sub = el("div", "cp-sub");
  sub.innerHTML = `<b>${escapeHtml(c.project || "—")}</b> · ${escapeHtml(c.agent || "agent")} · ${relTime(c.ts)}` +
    (c.handoffTo ? ` · handoff → <b>${escapeHtml(c.handoffTo)}</b>` : "");
  head.append(sub);

  const g = c.git;
  if (g && (g.branch || g.commit)) {
    const gd = el("div", "cp-git");
    const dirty = g.dirty > 0;
    gd.innerHTML = `<span class="sha">${escapeHtml(g.branch || "?")}@${escapeHtml(g.commit || "?")}</span>` +
      (g.subject ? " — " + escapeHtml(g.subject) : "") + "<br>" +
      (dirty
        ? `<span class="dirty">dirty</span> ${g.dirty} file${g.dirty > 1 ? "s" : ""} (+${g.insertions}/−${g.deletions})`
        : `<span class="clean">clean</span>`) +
      (g.worktree ? ` · worktree ${escapeHtml(g.worktree)}` : "");
    head.append(gd);
  }
  box.append(head);

  if (c.state) box.append(cpBlock(null, "State", c.state));
  if (c.verified && c.verified.length) box.append(cpList("verified", "Verified — safe to continue from", c.verified));
  if (c.blockers && c.blockers.length) box.append(cpList("blockers", "Known broken — do not build on it", c.blockers));
  if (c.decisions && c.decisions.length) box.append(cpList(null, "Decisions", c.decisions));
  if (c.failed && c.failed.length) box.append(cpList(null, "Ruled out", c.failed));
  if (c.commands && c.commands.length) box.append(cpList(null, "Shown by running", c.commands, true));
  if (c.questions && c.questions.length) box.append(cpList(null, "Open questions", c.questions));
  if (c.files && c.files.length) box.append(cpList(null, "Files touched", c.files, true));
  if (c.next) box.append(cpBlock(null, "Next", c.next));
}

async function loadSessions() {
  const list = $("sessions-list");
  list.innerHTML = '<div class="empty"><div class="big">⋯</div>reading sessions/…</div>';
  let cps = [];
  try {
    cps = await go().Checkpoints();
  } catch (err) {
    list.innerHTML = "";
    list.append(el("div", "empty", "⚠ " + err));
    return;
  }
  if (!cps || !cps.length) {
    list.innerHTML = "";
    list.append(el(
      "div", "empty",
      "No checkpoints yet — an agent calling checkpoint (or brain resume from the CLI) writes one to sessions/, and it will show up here, newest first."
    ));
    $("sessions-detail").innerHTML =
      '<div class="empty"><div class="big">◫</div>Nothing to show until a checkpoint exists.</div>';
    return;
  }
  list.innerHTML = "";
  cps.forEach((c, i) => list.append(renderSessionRow(c, i === 0)));
  renderCheckpointDetail(cps[0]);
}

// ---- context: what an arriving agent would receive ----

let ctxProjectsLoaded = false;

async function primeContextProjects() {
  if (ctxProjectsLoaded) return;
  ctxProjectsLoaded = true;
  try {
    const projects = await go().Projects();
    if (!projects || !projects.length) return;
    const dl = document.createElement("datalist");
    dl.id = "ctx-projects";
    projects.forEach((p) => {
      const o = document.createElement("option");
      o.value = p;
      dl.append(o);
    });
    document.body.append(dl);
    $("ctx-hint").setAttribute("list", "ctx-projects");
  } catch (_) {}
}

function ctxMetaRow(meta, k, v) {
  const s = document.createElement("span");
  s.innerHTML = `${escapeHtml(k)} <b>${escapeHtml(String(v))}</b>`;
  meta.append(s);
}

function renderContext(res) {
  const box = $("context-result");
  box.innerHTML = "";
  const p = res.pack || {};

  const meta = el("div", "ctx-meta");
  ctxMetaRow(meta, "project", p.project ? (p.project.name || p.project.slug) : "(unresolved)");
  ctxMetaRow(meta, "checkpoint", p.checkpoint ? ("yes" + (p.inherited ? " (inherited)" : "")) : "none");
  ctxMetaRow(meta, "notes", (p.notes || []).length);
  ctxMetaRow(meta, "memories", (p.related || []).length + (p.preferences || []).length);
  ctxMetaRow(meta, "open loops", (p.open_loops || []).length);
  box.append(meta);

  const budget = p.budget || { limit: 0, spent: 0, by: [] };
  const pct = budget.limit ? Math.min(100, Math.round((100 * budget.spent) / budget.limit)) : 0;
  const bwrap = el("div", "ctx-budget");
  bwrap.append(document.createTextNode(`budget ${budget.spent} / ${budget.limit} tok`));
  const track = el("div", "ctx-budget-track");
  const fill = el("div", "ctx-budget-fill");
  fill.style.width = pct + "%";
  track.append(fill);
  bwrap.append(track);
  box.append(bwrap);

  if (budget.by && budget.by.length) {
    const sec = el("div", "ctx-sections");
    budget.by.forEach((l) => {
      const t = el("span", "ctx-tag");
      t.innerHTML = `${escapeHtml(l.section)} <b>${l.items}</b>` +
        (l.dropped ? ` <span style="color:var(--bad)">−${l.dropped} dropped</span>` : "");
      sec.append(t);
    });
    box.append(sec);
  }

  const raw = el("div", "ctx-raw");
  raw.innerHTML = mdToHtml(res.markdown || "_(empty pack)_");
  box.append(raw);
}

function wireContext() {
  $("ctx-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const hint = $("ctx-hint").value.trim();
    const task = $("ctx-task").value.trim();
    const box = $("context-result");
    box.innerHTML = '<div class="empty"><div class="big">⋯</div>assembling — building the same pack contextpack.Build assembles for an arriving agent…</div>';
    try {
      const res = await go().ContextPreview(hint, task);
      renderContext(res);
    } catch (err) {
      box.innerHTML = "";
      box.append(el("div", "empty", "⚠ " + err));
    }
  });
}

// ---- memory: what the assistant has learned ----

let allMemories = [];
let memKindFilter = null;

function confBar(v) {
  const wrap = el("span", "conf-bar");
  const fill = document.createElement("span");
  fill.style.width = Math.round((v || 0) * 100) + "%";
  wrap.append(fill);
  return wrap;
}

function renderMemHealth(h) {
  const box = $("mem-health");
  box.innerHTML = "";
  const add = (k, v, cls) => {
    const s = document.createElement("span");
    s.innerHTML = `${escapeHtml(k)} <b class="${cls || ""}">${escapeHtml(String(v))}</b>`;
    box.append(s);
  };
  const scoreCls = h.score >= 0.8 ? "score-good" : h.score < 0.5 ? "score-warn" : "";
  add("total", h.total);
  add("score", Math.round((h.score || 0) * 100) + "%", scoreCls);
  add("duplicates", h.duplicates);
  add("stale", h.stale);
  add("low confidence", h.low_confidence);
  add("orphans", h.orphans);
}

function renderMemFilters(mems) {
  const box = $("mem-filters");
  box.innerHTML = "";
  const counts = {};
  mems.forEach((m) => { counts[m.kind] = (counts[m.kind] || 0) + 1; });
  const mk = (label, kind) => {
    const c = el("button", "chip", label);
    c.type = "button";
    c.setAttribute("aria-pressed", String(memKindFilter === kind));
    c.onclick = () => { memKindFilter = kind; renderMemFilters(mems); renderMemList(); };
    return c;
  };
  box.append(mk(`all (${mems.length})`, null));
  Object.keys(counts).sort().forEach((k) => box.append(mk(`${k} (${counts[k]})`, k)));
}

function renderMemList() {
  const box = $("mem-list");
  box.innerHTML = "";
  if (!allMemories.length) {
    box.append(el(
      "div", "empty",
      "No memories yet — they accumulate as the assistant learns preferences, people, and standing context from conversations, or from brain remember / the MCP remember tool."
    ));
    return;
  }
  const shown = memKindFilter ? allMemories.filter((m) => m.kind === memKindFilter) : allMemories;
  if (!shown.length) {
    box.append(el("div", "empty", `No ${memKindFilter} memories.`));
    return;
  }
  const sorted = shown.slice().sort((a, b) => b.created - a.created);
  for (const m of sorted) {
    const row = el("div", "mem-row");
    row.append(el("div", "kind", m.kind));
    const body = el("div", "body");
    body.append(el("div", "txt", m.text));
    const meta = el("div", "meta");
    if (m.project) meta.append(el("span", "proj", m.project));
    meta.append(document.createTextNode(m.source || "unknown source"));
    const confSpan = document.createElement("span");
    confSpan.append(confBar(m.confidence), document.createTextNode(Math.round((m.confidence || 0) * 100) + "%"));
    meta.append(confSpan);
    meta.append(document.createTextNode((m.uses || 0) + " use" + (m.uses === 1 ? "" : "s")));
    meta.append(document.createTextNode(relTime(m.created)));
    body.append(meta);
    row.append(body);
    const x = el("div", "x", "×");
    x.title = "forget";
    x.onclick = async () => { await go().ForgetMemory(m.id); await loadMemory(); };
    row.append(x);
    box.append(row);
  }
}

async function loadMemory() {
  const listBox = $("mem-list");
  listBox.innerHTML = '<div class="empty"><div class="big">⋯</div>loading memories…</div>';
  try {
    const [health, mems] = await Promise.all([go().MemoryHealth(), go().Memories()]);
    renderMemHealth(health);
    allMemories = mems || [];
    renderMemFilters(allMemories);
    renderMemList();
  } catch (err) {
    listBox.innerHTML = "";
    listBox.append(el("div", "empty", "⚠ " + err));
  }
}

// ---- brief: the secretary leading ----

async function loadBrief() {
  const b = await go().Brief();
  let pres = null;
  try { pres = await go().Presence(); } catch {}
  const box = $("brief");
  box.innerHTML = "";

  const greet = el("div", "greeting", userName ? `${b.greeting}, ${userName}.` : b.greeting + ".");
  if (pres && pres.name) greet.append(el("span", "presence-name", " — " + pres.name));
  box.append(greet);

  if (pres && pres.nudge) {
    const n = el("div", "presence-banner" + (pres.nudge.critical ? " critical" : ""));
    n.append(el("div", "main", pres.nudge.text));
    if (pres.nudge.detail) n.append(el("div", "detail", pres.nudge.detail));
    box.append(n);
  }

  const quiet = (!b.loops || !b.loops.length) &&
    (!b.dormant || !b.dormant.length) &&
    (!b.usual || !b.usual.length) && !b.review;
  if (quiet) {
    const c = el("div", "clear");
    c.append(el("div", "big", "✦"), el("div", null, "Nothing pressing — you're clear."));
    box.append(c);
    return;
  }

  if (b.upcoming && b.upcoming.length) {
    box.append(section("Coming up", b.upcoming.map(renderMeeting)));
  }
  if (b.loops && b.loops.length) {
    box.append(section("Open loops", b.loops.map(renderLoop)));
  }
  if (b.dormant && b.dormant.length) {
    box.append(section("Gone quiet", b.dormant.map(renderNudge)));
  }
  if (b.usual && b.usual.length) {
    box.append(section("Around now, you usually", b.usual.map(renderNudge)));
  }
  if (b.remembers && b.remembers.length) {
    box.append(section("Keeping in mind", b.remembers.map((m) => {
      const row = el("div", "nudge");
      row.append(el("div", "main", m));
      return row;
    })));
  }
  if (b.review > 0) {
    const r = el("div", "brief-review", `${b.review} proposal${b.review > 1 ? "s" : ""} waiting to review →`);
    r.onclick = () => show("review");
    box.append(r);
  }
}

function section(title, children) {
  const s = el("div", "brief-section");
  s.append(el("h3", null, title));
  children.forEach((c) => s.append(c));
  return s;
}

function renderMeeting(m) {
  const row = el("div", "loop meeting" + (m.imminent ? " imminent" : ""));
  row.append(el("div", "dot"));
  const txt = el("div", "txt");
  txt.append(el("div", "main", m.title));
  const sub = [];
  sub.push(m.in_min < 90 ? "in " + m.in_min + "m" : "at " + m.at);
  if (m.cal) sub.push(m.cal);
  txt.append(el("div", "sub", sub.join("  ·  ")));
  row.append(txt);
  return row;
}

function renderLoop(l) {
  const row = el("div", "loop" + (l.stale ? " stale" : ""));
  row.append(el("div", "dot"));

  const txt = el("div", "txt");
  txt.append(el("div", "main", l.text));
  const parts = [];
  if (l.age_days > 0) parts.push(l.age_days + "d open");
  if (l.who) parts.push("→ " + l.who);
  if (l.due) parts.push("(" + l.due + ")");
  if (parts.length) txt.append(el("div", "sub", parts.join("  ")));
  row.append(txt);

  const acts = el("div", "acts");
  const done = el("button", "mini done", "✓");
  done.title = "done";
  done.onclick = () => closeLoop(l.id, true, row);
  const drop = el("button", "mini", "×");
  drop.title = "not a task — stop showing this";
  drop.onclick = () => closeLoop(l.id, false, row);
  acts.append(done, drop);
  row.append(acts);
  return row;
}

function renderNudge(n) {
  const row = el("div", "nudge");
  row.append(el("div", "main", n.text));
  if (n.detail) row.append(el("div", "sub", n.detail));
  return row;
}

async function closeLoop(id, done, row) {
  await (done ? go().LoopDone(id) : go().LoopDrop(id));
  row.style.transition = "opacity .2s, transform .2s";
  row.style.opacity = "0";
  row.style.transform = "translateX(8px)";
  setTimeout(loadBrief, 200);
}

// ---- today ----

async function loadTimeline() {
  const items = await go().Timeline();
  const box = $("timeline");
  if (!items || items.length === 0) {
    box.innerHTML = '<div class="empty"><div class="big">◷</div>Nothing recorded today yet — captured app focus, commits, and URLs appear here as they\'re seen, if capture is running (check CAPTURE in the state strip above).</div>';
    return;
  }

  box.innerHTML = "";
  for (const it of items) {
    const row = el("div", "row");
    row.append(el("span", "t", it.time));
    const body = el("div", "body");
    if (it.app) body.append(el("div", "app", it.app));
    body.append(el("div", "label", it.label || ""));
    row.append(body);
    if (it.dur) row.append(el("span", "d", it.dur));
    box.append(row);
  }
}

// ---- review queue ----

async function loadQueue() {
  const items = await go().Proposals();
  const box = $("queue");
  if (!items || items.length === 0) {
    box.innerHTML = '<div class="empty"><div class="big">✓</div>Queue is empty — proposals appear here when the rollup pass finds something worth surfacing from captured activity.</div>';
    return;
  }
  box.innerHTML = "";
  items.forEach((p, i) => box.append(renderProposal(p, i === 0)));
  box.querySelector(".card")?.focus();
}

function renderProposal(p, focusable) {
  const card = el("div", "card");
  card.tabIndex = 0;
  card.dataset.id = p.id;

  card.append(el("div", "k", p.kind.replace("_", " ")));
  card.append(el("div", "s", p.summary));

  const meta = el("div", "meta");
  const bar = el("span", "conf");
  bar.style.width = Math.round(p.conf * 40) + "px";
  meta.append(bar, document.createTextNode(`  ${p.conf.toFixed(2)} · ${p.model}`));
  card.append(meta);

  const ev = el("div", "evidence");
  (p.evidence || []).forEach((line) => ev.append(el("div", null, line)));
  card.append(ev);

  const actions = el("div", "actions");
  const accept = el("button", "btn accept", "Accept");
  accept.onclick = () => decide(p.id, true, card);
  const reject = el("button", "btn", "Reject");
  reject.onclick = () => decide(p.id, false, card);
  const why = el("button", "btn link", "evidence");
  why.onclick = () => ev.classList.toggle("open");
  actions.append(accept, reject, why);
  card.append(actions);

  card.addEventListener("keydown", (e) => {
    switch (e.key) {
      case "a": case "Enter": decide(p.id, true, card); break;
      case "r": case "x": decide(p.id, false, card); break;
      case "e": ev.classList.toggle("open"); break;
      case "j": card.nextElementSibling?.focus(); break;
      case "k": card.previousElementSibling?.focus(); break;
      default: return;
    }
    e.preventDefault();
  });

  return card;
}

async function decide(id, accept, card) {
  const next = card.nextElementSibling || card.previousElementSibling;
  try {
    thinking(true);
    await (accept ? go().Accept(id) : go().Reject(id));
    card.style.transition = "opacity .2s, transform .2s";
    card.style.opacity = "0";
    card.style.transform = "translateX(" + (accept ? "" : "-") + "12px)";
    setTimeout(() => {
      card.remove();
      next?.focus();
      if (!$("queue").querySelector(".card"))
        $("queue").innerHTML = '<div class="empty"><div class="big">✓</div>all reviewed</div>';
    }, 200);
  } finally {
    thinking(false);
    refreshStatus();
  }
}

// ---- routines ----

async function loadRoutines() {
  const items = await go().Routines();
  const box = $("routines");
  if (!items || items.length === 0) {
    box.innerHTML = '<div class="empty"><div class="big">↻</div>No routines detected yet — a routine needs a few repeats of the same app or site at a similar time of day before it\'s confident enough to name.</div>';
    return;
  }

  box.innerHTML = "";
  for (const line of items) {
    const row = el("div", "row");
    const [name, when] = line.split(" · ");
    const body = el("div", "body");
    body.append(el("div", "app", name));
    body.append(el("div", "label", when || ""));
    row.append(body);
    box.append(row);
  }
}

// ---- ask ----

let streamingBubble = null;

// citeSpans turns [slug] into a styled citation. Applied after links so a real
// [text](url) isn't caught.
function citeSpans(s) {
  return s.replace(/\[([^\]\n]+)\]/g, '<span class="cite">[$1]</span>');
}

function streamRender(raw) {
  return citeSpans(escapeHtml(raw)).replace(/\n/g, "<br>");
}

function mdToHtml(src) {
  if (!src) return "";
  let s = escapeHtml(src);

  const blocks = [];
  s = s.replace(/```(?:\w+)?\n?([\s\S]*?)```/g, (_, code) => {
    blocks.push('<pre class="code"><code>' + code.replace(/\n$/, "") + "</code></pre>");
    return "\u0000" + (blocks.length - 1) + "\u0000";
  });

  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  s = s.replace(/^###\s+(.*)$/gm, "<h4>$1</h4>")
       .replace(/^##\s+(.*)$/gm, "<h3>$1</h3>")
       .replace(/^#\s+(.*)$/gm, "<h2>$1</h2>");
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
       .replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  s = s.replace(/\[([^\]]+)\]\((https?:[^)]+)\)/g, '<a href="$2">$1</a>');
  s = citeSpans(s);

  s = s.replace(/(?:^[-*]\s+.*(?:\n|$))+/gm, (m) =>
    "<ul>" + m.trim().split(/\n/).map((l) => "<li>" + l.replace(/^[-*]\s+/, "") + "</li>").join("") + "</ul>");
  s = s.replace(/(?:^\d+\.\s+.*(?:\n|$))+/gm, (m) =>
    "<ol>" + m.trim().split(/\n/).map((l) => "<li>" + l.replace(/^\d+\.\s+/, "") + "</li>").join("") + "</ol>");

  s = s.split(/\n{2,}/).map((chunk) =>
    /^\s*<(h\d|ul|ol|pre)/.test(chunk) ? chunk : "<p>" + chunk.replace(/\n/g, "<br>") + "</p>"
  ).join("");

  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => blocks[+i]);
}

function addBubble(role, text) {
  const wrap = $("chat-log");
  const b = el("div", "bubble " + role);
  if (role === "user") b.textContent = text;
  else b.innerHTML = mdToHtml(text);
  wrap.append(b);
  wrap.scrollTop = wrap.scrollHeight;
  return b;
}

function sendMessage(q) {
  if (!q) return;
  $("answer").hidden = true;
  addBubble("user", q);
  streamingBubble = addBubble("assistant", "");
  streamingBubble.dataset.raw = "";
  streamingBubble.classList.add("streaming");
  thinking(true);
  go().Send(q);
}

$("ask-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const q = $("ask").value.trim();
  if (!q) return;
  $("ask").value = "";
  sendMessage(q);
});

async function restoreHistory() {
  try {
    const turns = await go().History();
    (turns || []).forEach((t) => addBubble(t.role, t.content));
  } catch (_) {}
}

function toast(msg) {
  const t = el("div", "toast", msg);
  document.body.append(t);
  setTimeout(() => { t.style.opacity = "0"; setTimeout(() => t.remove(), 300); }, 3200);
}

async function wireVoice() {
  const btn = $("mic-btn");
  if (!btn) return;
  let ok = false;
  try { ok = await go().VoiceAvailable(); } catch (_) {}
  if (!ok) return;
  btn.hidden = false;
  btn.onclick = async () => {
    btn.classList.add("listening");
    btn.textContent = "…";
    try {
      const text = await go().VoiceInput();
      if (text) {
        const inp = $("ask");
        inp.value = text;
        inp.focus();
      } else {
        toast("Didn't catch that — try again.");
      }
    } catch (e) {
      toast("⚠ " + e);
    }
    btn.classList.remove("listening");
    btn.textContent = "🎤";
  };
}

function wireChat() {
  const rt = window.runtime;
  if (!rt || !rt.EventsOn) return;
  rt.EventsOn("chat:token", (tok) => {
    if (!streamingBubble) return;
    streamingBubble.dataset.raw += tok;
    streamingBubble.innerHTML = streamRender(streamingBubble.dataset.raw);
    $("chat-log").scrollTop = $("chat-log").scrollHeight;
  });
  rt.EventsOn("chat:done", () => {
    if (streamingBubble) {
      streamingBubble.innerHTML = mdToHtml(streamingBubble.dataset.raw);
      streamingBubble.classList.remove("streaming");
    }
    streamingBubble = null;
    thinking(false);
  });
  rt.EventsOn("chat:error", (msg) => {
    if (streamingBubble) {
      streamingBubble.classList.remove("streaming");
      streamingBubble.textContent = "⚠ " + msg;
    }
    streamingBubble = null;
    thinking(false);
  });
  rt.EventsOn("memory:learned", (n) => toast("🧠 learned " + n + " new thing" + (n > 1 ? "s" : "")));
}

// ---- setup / settings: name yourself, name the assistant ----

let userName = "";

async function openSetup(editing) {
  const id = await go().Identity();
  $("setup-user").value = id.userName || "";
  $("setup-agent").value = id.agentName || "";
  $("setup-title").textContent = editing ? "Settings" : "Welcome";
  $("setup-sub").textContent = editing
    ? "Change what you're called, or rename your assistant."
    : "Let's get your assistant set up — this only takes a moment.";
  $("setup-save").textContent = editing ? "Save" : "Get started";
  $("setup-cancel").hidden = !editing;
  $("setup").hidden = false;
  $("setup-user").focus();
}

function closeSetup() { $("setup").hidden = true; }

async function saveSetup() {
  const user = $("setup-user").value;
  const agent = $("setup-agent").value;
  try {
    await go().SaveIdentity(user, agent);
  } catch (err) {
    toast("⚠ " + err);
    return;
  }
  userName = (user || "").trim();
  closeSetup();
  if (current === "brief") loadBrief();
}

function wireSetup() {
  $("gear-btn").onclick = () => openSetup(true);
  $("setup-save").onclick = saveSetup;
  $("setup-cancel").onclick = closeSetup;
  $("setup").addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); saveSetup(); }
  });
}

// ---- tabs ----

const TAB_ORDER = ["sessions", "context", "memory", "brief", "today", "review", "routines", "graph"];

function show(tab) {
  current = tab;
  document.querySelectorAll(".tab").forEach((t) =>
    t.setAttribute("aria-selected", t.dataset.tab === tab));
  document.querySelectorAll(".panel").forEach((p) =>
    p.classList.toggle("active", p.id === "panel-" + tab));

  if (tab === "sessions") loadSessions();
  if (tab === "context") primeContextProjects();
  if (tab === "memory") loadMemory();
  if (tab === "brief") loadBrief();
  if (tab === "today") loadTimeline();
  if (tab === "review") loadQueue();
  if (tab === "routines") loadRoutines();
  if (tab === "graph") GraphView.open();
}

document.querySelectorAll(".tab").forEach((t) =>
  t.addEventListener("click", () => show(t.dataset.tab)));

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    const setup = $("setup");
    if (setup && !setup.hidden) {
      if (!$("setup-cancel").hidden) closeSetup();
      return;
    }
    go()?.Hide?.();
    return;
  }
  if (document.activeElement.tagName === "INPUT") return;
  const n = parseInt(e.key, 10);
  if (n >= 1 && n <= TAB_ORDER.length) show(TAB_ORDER[n - 1]);
});

// ---- boot ----

async function maybeOnboard() {
  try {
    const id = await go().Identity();
    userName = (id.userName || "").trim();
    if (!id.onboarded) { await openSetup(false); return; }
  } catch (_) {}
}

window.addEventListener("DOMContentLoaded", () => {
  refreshStatus();
  refreshOverview();
  wireChat();
  wireVoice();
  wireSetup();
  wireContext();
  restoreHistory();
  show("sessions");
  maybeOnboard();
  // Pending-review badge and orb need to feel live; overview counts vault
  // state that changes far less often, so it polls on a longer cycle.
  setInterval(refreshStatus, 4000);
  setInterval(refreshOverview, 15000);
});
