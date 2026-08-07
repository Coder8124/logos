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
    // Recording wins over every other state: it is a safety signal.
    orb.className = "orb " + (s.recording ? "recording" : "idle");
    orb.title = s.recording ? "recording" : "idle";

    // The review badge counts note proposals AND outbound actions awaiting you.
    const badge = $("pending-badge");
    const waiting = (s.pending || 0) + (s.actions || 0);
    if (waiting > 0) {
      badge.textContent = waiting;
      badge.hidden = false;
      // Actions are higher-stakes; tint the badge red when any are pending.
      badge.style.background = s.actions > 0 ? "var(--bad)" : "var(--accent)";
    } else {
      badge.hidden = true;
    }

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

  const greet = el("div", "greeting", b.greeting + ".");
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
  await loadActions();
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

// Outbound actions are the higher-stakes half of the trust loop: they touch the
// world, so they lead the review queue and are styled as a warning.
async function loadActions() {
  const box = $("actions-queue");
  box.innerHTML = "";
  let actions = [];
  try { actions = await go().PendingActions(); } catch (_) {}
  if (!actions || !actions.length) return;

  box.append(el("div", "actions-head", "⚡ Awaiting your confirmation — these act on the outside world"));
  for (const act of actions) box.append(renderAction(act));
}

function renderAction(act) {
  const card = el("div", "action-card");
  card.append(el("div", "k", act.kind.replace("_", " ")));
  card.append(el("div", "s", act.title));
  if (act.preview) card.append(el("div", "prev", act.preview));

  const actions = el("div", "actions");
  const approve = el("button", "btn danger", "Approve & run");
  approve.onclick = async () => {
    approve.textContent = "…";
    try {
      const res = await go().ApproveAction(act.id);
      toast("✓ " + res);
    } catch (e) {
      toast("⚠ " + e);
    }
    loadActions();
    refreshStatus();
  };
  const reject = el("button", "btn", "Reject");
  reject.onclick = async () => { await go().RejectAction(act.id); loadActions(); refreshStatus(); };
  actions.append(approve, reject);
  card.append(actions);
  return card;
}

// ---- business panel ----

let bizFile = "";

function wireBusiness() {
  $("biz-pick").onclick = async () => {
    const path = await go().PickSpreadsheet();
    if (!path) return;
    bizFile = path;
    $("biz-file").textContent = path.split("/").pop();
    $("biz-actions").hidden = false;
  };
  document.querySelectorAll("#biz-actions .btn").forEach((b) =>
    b.addEventListener("click", () => runBizOp(b.dataset.op)));
}

async function runBizOp(op) {
  if (!bizFile) return;
  const out = $("biz-out");
  out.innerHTML = '<div class="empty">working…</div>';
  thinking(true);
  try {
    let text;
    if (op === "read") text = await go().BizRead(bizFile);
    else if (op === "verify") text = await go().BizVerify(bizFile);
    else if (op === "analyze") text = await go().BizAnalyze(bizFile, "");
    else if (op === "expense") text = await go().BizExpenseReport(bizFile);
    out.innerHTML = "";
    const pre = el("pre", "answer");
    pre.style.padding = "12px";
    pre.textContent = text;
    out.append(pre);
  } catch (e) {
    out.innerHTML = '<div class="empty">' + e + "</div>";
  } finally {
    thinking(false);
  }
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

function addBubble(role, text) {
  const wrap = $("chat-log");
  const b = el("div", "bubble " + role);
  b.innerHTML = role === "assistant"
    ? (text || "").replace(/\[([^\]]+)\]/g, '<span class="cite">[$1]</span>')
    : "";
  if (role === "user") b.textContent = text;
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

function wireRecording() {
  const btn = $("rec-btn");
  btn.onclick = async () => {
    const state = await go().ToggleRecording("");
    setRecState(state);
  };
  const rt = window.runtime;
  if (rt && rt.EventsOn) {
    rt.EventsOn("record:started", () => setRecState("recording"));
    rt.EventsOn("record:processing", () => setRecState("processing"));
    rt.EventsOn("record:hotkey", (s) => setRecState(s));
    rt.EventsOn("record:done", (r) => {
      setRecState("idle");
      toast(`✓ saved "${r.title}" — ${r.cards} cards`);
    });
    rt.EventsOn("record:error", (m) => { setRecState("idle"); toast("⚠ " + m); });
  }
}

function setRecState(state) {
  const btn = $("rec-btn");
  btn.className = "rec-btn" + (state === "recording" ? " recording" : state === "processing" ? " processing" : "");
  btn.textContent = state === "recording" ? "● Stop" : state === "processing" ? "…" : "● Rec";
}

function toast(msg) {
  const t = el("div", "toast", msg);
  document.body.append(t);
  setTimeout(() => { t.style.opacity = "0"; setTimeout(() => t.remove(), 300); }, 3200);
}

function wireChat() {
  const rt = window.runtime;
  if (!rt || !rt.EventsOn) return;
  rt.EventsOn("chat:token", (tok) => {
    if (!streamingBubble) return;
    streamingBubble.dataset.raw += tok;
    streamingBubble.innerHTML = streamingBubble.dataset.raw
      .replace(/\[([^\]]+)\]/g, '<span class="cite">[$1]</span>');
    $("chat-log").scrollTop = $("chat-log").scrollHeight;
  });
  rt.EventsOn("chat:done", () => {
    if (streamingBubble) streamingBubble.classList.remove("streaming");
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

// ---- flavors ----

let activeFlavor = "secretary";

async function loadFlavors() {
  const f = await go().Flavor();
  activeFlavor = f.active;
  const box = $("flavors");
  box.innerHTML = "";
  for (const name of f.all) {
    const pill = el("button", "flavor-pill" + (name === f.active ? " on" : ""), name);
    pill.onclick = () => switchFlavor(name);
    box.append(pill);
  }
  // Tutor and business get their own tabs; show them only in that flavor so the
  // panel stays focused on the persona you're in.
  showTabFor("tutor", f.active === "tutor");
  showTabFor("business", f.active === "business");
}

function showTabFor(name, on) {
  const tab = document.querySelector(`.tab[data-tab="${name}"]`);
  if (tab) tab.hidden = !on;
}

async function switchFlavor(name) {
  await go().SetFlavor(name);
  await loadFlavors();
  // Land on the persona's home surface.
  if (name === "tutor") { show("tutor"); loadTutorHeader(); }
  else if (name === "business") show("business");
  else show("brief");
}

// ---- tutor panel ----

$("study-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const topic = $("study-topic").value.trim();
  if (!topic) return;
  const out = $("tutor-out");
  out.innerHTML = '<div class="empty">thinking…</div>';
  thinking(true);
  try {
    const cards = await go().Quiz(topic);
    out.innerHTML = "";
    const save = el("button", "btn accept", "＋ Save as flashcards");
    save.style.marginBottom = "10px";
    save.onclick = async () => {
      const n = await go().AddCards(topic);
      save.textContent = `added ${n} to your deck`;
      save.disabled = true;
      loadTutorHeader();
    };
    out.append(save);
    cards.forEach((c) => out.append(renderQCard(c)));
  } catch (err) {
    out.innerHTML = '<div class="empty">' + err + "</div>";
  } finally {
    thinking(false);
  }
});

function renderQCard(c) {
  const card = el("div", "qcard");
  card.append(el("div", "q", c.q));
  const reveal = el("button", "reveal", "show answer");
  const ans = el("div", "a", c.a);
  reveal.onclick = () => {
    ans.classList.toggle("shown");
    reveal.textContent = ans.classList.contains("shown") ? "hide answer" : "show answer";
  };
  card.append(reveal, ans);
  if (c.source) card.append(el("div", "src", c.source));
  return card;
}

// Spaced-repetition review of due cards (TurboLearn-inspired; see CREDITS).
let reviewQueue = [];
async function startReview() {
  reviewQueue = await go().DueCards();
  const out = $("tutor-out");
  if (!reviewQueue.length) {
    out.innerHTML = '<div class="empty">no cards due — make some with a topic above</div>';
    return;
  }
  showNextCard();
}

function showNextCard() {
  const out = $("tutor-out");
  if (!reviewQueue.length) {
    out.innerHTML = '<div class="clear"><div class="big">✓</div>all reviewed for now</div>';
    loadTutorHeader();
    return;
  }
  const c = reviewQueue[0];
  out.innerHTML = "";
  const card = el("div", "qcard");
  card.append(el("div", "q", c.Q || c.q));
  const ans = el("div", "a", c.A || c.a);
  const reveal = el("button", "reveal", "show answer");
  reveal.onclick = () => {
    ans.classList.add("shown");
    reveal.remove();
    const rate = el("div", "actions");
    rate.style.marginTop = "10px";
    [["Again", 1], ["Good", 2], ["Easy", 3]].forEach(([label, g]) => {
      const b = el("button", "btn" + (g === 2 ? " accept" : ""), label);
      b.onclick = async () => {
        await go().GradeCard(c.ID || c.id, g);
        reviewQueue.shift();
        showNextCard();
      };
      rate.append(b);
    });
    card.append(rate);
  };
  card.append(reveal, ans);
  out.append(card);
}

async function loadTutorHeader() {
  // Show a "review N due" affordance when cards are waiting.
  try {
    const due = await go().DueCards();
    let btn = $("review-btn");
    if (due.length && !btn) {
      btn = el("button", "btn accept", `Review ${due.length} due`);
      btn.id = "review-btn";
      btn.style.margin = "0 12px 8px";
      btn.onclick = startReview;
      $("panel-tutor").insertBefore(btn, $("tutor-out"));
    } else if (btn) {
      if (due.length) btn.textContent = `Review ${due.length} due`;
      else btn.remove();
    }
  } catch (_) {}
}

// ---- business panel ----

$("biz-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = $("biz-q").value.trim();
  if (!q) return;
  const out = $("biz-out");
  // The goal box runs the agent harness — it orchestrates the tools, and any
  // outbound action it decides on queues for approval in Review.
  out.innerHTML = '<div class="empty">on it…</div>';
  const steps = el("div", "meta");
  steps.style.padding = "0 12px 8px";
  thinking(true);
  go().RunAgent(q);
  const rt = window.runtime;
  if (rt && rt.EventsOn) {
    out.innerHTML = "";
    out.append(steps);
    rt.EventsOff && rt.EventsOff("agent:step", "agent:done", "agent:error");
    rt.EventsOn("agent:step", (tool) => { steps.textContent += "→ " + tool + "  "; });
    rt.EventsOn("agent:done", (answer) => {
      const div = el("div", "answer");
      div.style.padding = "12px";
      div.textContent = answer;
      out.append(div);
      thinking(false);
      refreshStatus();
    });
    rt.EventsOn("agent:error", (m) => { out.innerHTML = '<div class="empty">' + m + "</div>"; thinking(false); });
  }
});

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

// ---- idle help: offer a hand when the tutor thinks you're stuck ----

let helpDismissedUntil = 0;

async function pollIdleHelp() {
  if (activeFlavor !== "tutor") return;
  if (Date.now() < helpDismissedUntil) return;
  if (!$("help-offer").hidden) return; // already showing

  try {
    if (await go().ShouldOfferHelp()) {
      $("help-body").hidden = true;
      $("help-body").textContent = "";
      $("help-offer").hidden = false;
    }
  } catch (_) {}
}

$("help-yes").onclick = async () => {
  const body = $("help-body");
  body.hidden = false;
  body.textContent = "reading your screen…";
  thinking(true);
  try {
    body.textContent = await go().HelpNow();
  } catch (err) {
    body.textContent = "couldn't help: " + err;
  } finally {
    thinking(false);
  }
};

$("help-no").onclick = () => {
  $("help-offer").hidden = true;
  // Don't nag: stay quiet for a few minutes after a decline.
  helpDismissedUntil = Date.now() + 5 * 60 * 1000;
};

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
  if (e.key === "Escape") { go()?.Hide?.(); return; }
  if (document.activeElement.tagName === "INPUT") return;
  if (e.key === "1") show("brief");
  if (e.key === "2") show("today");
  if (e.key === "3") show("review");
  if (e.key === "4") show("routines");
});

// ---- boot ----

window.addEventListener("DOMContentLoaded", () => {
  refreshStatus();
  loadFlavors();
  wireChat();
  wireRecording();
  wireBusiness();
  wireMemory();
  restoreHistory();
  show("brief");
  // The idle-help offer only ever appears in tutor mode; the poller checks that
  // itself, so it is safe to run always.
  setInterval(pollIdleHelp, 5000);
  // Poll status so the pending badge and recording orb stay live while the
  // panel is open. Cheap; the queries are indexed counts.
  setInterval(refreshStatus, 4000);
});
