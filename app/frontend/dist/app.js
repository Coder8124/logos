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

let current = "brief";

// ---- orb / status ----

async function refreshStatus() {
  try {
    const s = await go().Status();
    $("runtime").textContent = s.runtime || "";

    const orb = $("orb");
    orb.className = "orb idle";

    const badge = $("pending-badge");
    const waiting = s.pending || 0;
    badge.textContent = waiting;
    badge.hidden = waiting === 0;

    // Memory chip: what the assistant has learned about you.
    const chip = $("mem-chip");
    if (s.memories > 0) {
      chip.textContent = "🧠 " + s.memories;
      chip.hidden = false;
    } else {
      chip.hidden = true;
    }
  } catch (_) {
    // Backend not up yet during dev; leave the UI in its resting state.
  }
}

function thinking(on) {
  const orb = $("orb");
  if (on) orb.className = "orb thinking";
  else refreshStatus();
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

  // The presence's single most pressing nudge, surfaced as a banner. A critical
  // one (an imminent meeting) reads red; everything else is a gentle prompt.
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

  // Meetings lead: they are the only items with a deadline you can't recover
  // from missing.
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
  if (!items || items.length === 0) return;

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
    box.innerHTML = "";
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

  // j/k to move, enter/a accept, r/x reject, e toggle evidence — the same keys
  // as the CLI review, so muscle memory transfers between surfaces.
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
  if (!items || items.length === 0) return;

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

// The assistant is conversational and streams. This is what makes it respond
// like an agent rather than a search box: the reply fills in token by token,
// keeps the thread, and knows your current context.
let streamingBubble = null;

function escapeHtml(s) {
  return (s || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// citeSpans turns [slug] into a styled citation. Applied after links so a real
// [text](url) isn't caught.
function citeSpans(s) {
  return s.replace(/\[([^\]\n]+)\]/g, '<span class="cite">[$1]</span>');
}

// streamRender is the light pass used while tokens are still arriving: escape,
// citations, and line breaks. Full markdown is applied once the answer is done,
// so half-written syntax never flickers mid-stream.
function streamRender(raw) {
  return citeSpans(escapeHtml(raw)).replace(/\n/g, "<br>");
}

// mdToHtml is a small dependency-free markdown renderer for the model's answers —
// the assistant replies in markdown and the panel should read it as such, not show
// the asterisks and hashes raw. Covers what an LLM actually emits: code, headers,
// bold/italic, lists, links, and citations.
function mdToHtml(src) {
  if (!src) return "";
  let s = escapeHtml(src);

  // Fenced code blocks, stashed so nothing else rewrites their insides.
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

  // Group consecutive bullet / numbered lines into a single list.
  s = s.replace(/(?:^[-*]\s+.*(?:\n|$))+/gm, (m) =>
    "<ul>" + m.trim().split(/\n/).map((l) => "<li>" + l.replace(/^[-*]\s+/, "") + "</li>").join("") + "</ul>");
  s = s.replace(/(?:^\d+\.\s+.*(?:\n|$))+/gm, (m) =>
    "<ol>" + m.trim().split(/\n/).map((l) => "<li>" + l.replace(/^\d+\.\s+/, "") + "</li>").join("") + "</ol>");

  // Paragraphs on blank lines; single newlines become <br>. Block elements pass
  // through unwrapped.
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

// Wire the streaming events once the Wails runtime is present.
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

// wireVoice enables speak-to-type: a mic button that records a turn, transcribes
// it locally, and drops the text into the chat input. Shown only when the local
// STT toolchain is present, so it never advertises a capability that isn't there.
async function wireVoice() {
  const btn = $("mic-btn");
  if (!btn) return;
  let ok = false;
  try { ok = await go().VoiceAvailable(); } catch (_) {}
  if (!ok) return; // no STT: leave the button hidden
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
        // Heard nothing intelligible. Say so — a button that quietly does
        // nothing reads as broken.
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
    // Light render while tokens arrive, so half-written markdown never flickers.
    streamingBubble.innerHTML = streamRender(streamingBubble.dataset.raw);
    $("chat-log").scrollTop = $("chat-log").scrollHeight;
  });
  rt.EventsOn("chat:done", () => {
    if (streamingBubble) {
      // Full markdown render once the answer is complete.
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
}

// ---- setup / settings: name yourself, name the assistant ----

// What the assistant calls the user, for a warmer greeting. Set on boot from
// the saved identity; empty keeps the greeting impersonal.
let userName = "";

// openSetup fills the sheet from the saved identity and shows it. editing=false
// is the first-run welcome (no cancel, "Get started"); editing=true is the gear
// (cancellable, "Save").
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
  loadBrief();
  refreshStatus();
}

function wireSetup() {
  $("gear-btn").onclick = () => openSetup(true);
  $("setup-save").onclick = saveSetup;
  $("setup-cancel").onclick = closeSetup;
  // Enter anywhere in the sheet commits it — it is small and every field is
  // optional.
  $("setup").addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); saveSetup(); }
  });
}

// On boot: greet a first-time user with the setup screen before anything else,
// and learn their name so the greeting can use it.
async function maybeOnboard() {
  try {
    const id = await go().Identity();
    userName = (id.userName || "").trim();
    if (!id.onboarded) { await openSetup(false); return; }
    loadBrief(); // repaint the greeting now that we know the name
  } catch (_) {}
}

// ---- memory: the assistant's persistent knowledge of you ----

function wireMemory() {
  const chip = $("mem-chip");
  chip.style.cursor = "pointer";
  chip.onclick = showMemories;
  const rt = window.runtime;
  if (rt && rt.EventsOn) {
    rt.EventsOn("memory:learned", (n) => toast("🧠 learned " + n + " new thing" + (n > 1 ? "s" : "")));
  }
}

async function showMemories() {
  const mems = await go().Memories();
  const box = $("brief");
  show("brief");
  box.innerHTML = "";
  box.append(el("div", "greeting", "What I remember"));
  if (!mems || !mems.length) {
    box.append(el("div", "clear", "nothing yet — I learn as we talk."));
    return;
  }
  const sec = el("div", "brief-section");
  for (const m of mems) {
    const row = el("div", "mem-row");
    row.append(el("div", "kind", m.kind));
    row.append(el("div", "txt", m.text));
    const x = el("div", "x", "×");
    x.onclick = async () => { await go().ForgetMemory(m.id); showMemories(); };
    row.append(x);
    sec.append(row);
  }
  box.append(sec);
}

// ---- tabs ----

function show(tab) {
  current = tab;
  document.querySelectorAll(".tab").forEach((t) =>
    t.setAttribute("aria-selected", t.dataset.tab === tab));
  document.querySelectorAll(".panel").forEach((p) =>
    p.classList.toggle("active", p.id === "panel-" + tab));

  if (tab === "brief") loadBrief();
  if (tab === "today") loadTimeline();
  if (tab === "review") loadQueue();
  if (tab === "routines") loadRoutines();
  if (tab === "graph") GraphView.open();
  // tutor and business panels load lazily on their own forms
}

document.querySelectorAll(".tab").forEach((t) =>
  t.addEventListener("click", () => show(t.dataset.tab)));

// Number keys jump between tabs from anywhere.
document.addEventListener("keydown", (e) => {
  // Esc dismisses the panel — the frameless window has no close button, so this
  // is how it gets out of the way. Relaunching brings it back (single instance).
  if (e.key === "Escape") {
    const setup = $("setup");
    if (setup && !setup.hidden) {
      // The welcome screen (no Cancel) ignores Esc; the editable settings sheet
      // dismisses on it rather than closing the whole panel.
      if (!$("setup-cancel").hidden) closeSetup();
      return;
    }
    go()?.Hide?.();
    return;
  }
  if (document.activeElement.tagName === "INPUT") return;
  if (e.key === "1") show("brief");
  if (e.key === "2") show("today");
  if (e.key === "3") show("review");
  if (e.key === "4") show("routines");
});

// ---- boot ----

window.addEventListener("DOMContentLoaded", () => {
  refreshStatus();
  wireChat();
  wireVoice();
  wireMemory();
  wireSetup();
  restoreHistory();
  show("brief");
  // First run shows the welcome screen; a returning user just gets their name
  // folded into the greeting.
  maybeOnboard();
  // Poll status so the pending badge stays live while the panel is open.
  // Cheap; the queries are indexed counts.
  setInterval(refreshStatus, 4000);
});
