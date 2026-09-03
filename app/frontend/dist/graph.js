// Memory graph: a canvas force-directed renderer, ego-mode only.
//
// The backend hands us a small neighbourhood (a focus node and a couple of
// hops), so a plain JS force simulation is smooth — the design's "layout in the
// backend" concern was about the full 5k-node graph, which we never draw. Here
// the value is interactivity: drag nodes, click to re-centre, scrub time.

const GraphView = (() => {
  // These identify a node's kind, not the app's meaning-bearing accents — good
  // and bad stay reserved for verified/blocked, so this stays a separate,
  // deliberately more colourful palette. What DOES have to track the theme is
  // anything that assumed a dark background: label text, the selection ring,
  // and the two kinds ("missing", "note"/"daily") that were tuned for it.
  const KIND_COLORS = {
    person: "#6b78e8", project: "#34d399", topic: "#f59e0b",
    routine: "#8b6cf0", source: "#22d3ee", study: "#f472b6", org: "#f87171",
  };

  let canvas, ctx, dpr = 1;
  let nodes = [], edges = [], focus = "";
  let hops = 2, similarity = false;
  let selected = null, hovered = null, dragging = null;
  let raf = null, alpha = 1;
  let timeCut = Infinity; // time scrubber: hide nodes first_seen after this
  let tip = null;
  const cx = () => canvas.clientWidth / 2;
  const cy = () => canvas.clientHeight / 2;

  // theme() reads the live token values every frame — cheap for an ego graph's
  // handful of nodes — so a toggle between light and dark repaints correctly
  // without the view needing to know it happened.
  function theme() {
    const cs = getComputedStyle(document.documentElement);
    const v = (name, fallback) => (cs.getPropertyValue(name).trim() || fallback);
    return {
      ink: v("--ink", "#e8e8ea"),
      ink3: v("--ink-3", "#8a8f9a"),
      patina: v("--patina", "#5fb3a3"),
      rule: v("--rule", "#3a3f4b"),
      font: v("--f-body", "-apple-system, sans-serif"),
    };
  }

  function color(kind, t) {
    if (kind === "daily" || kind === "note") return t.ink3;
    if (kind === "missing") return t.rule;
    return KIND_COLORS[kind] || t.ink3;
  }
  function radius(n) { return 5 + Math.min(9, Math.sqrt(n.degree || 1) * 2.2); }

  async function load(newFocus) {
    const g = await window.go.main.App.GraphView(newFocus || "", hops, similarity);
    focus = g.focus;
    const prev = {};
    nodes.forEach((n) => (prev[n.slug] = n));
    nodes = (g.nodes || []).map((n) => {
      const p = prev[n.slug];
      return Object.assign(n, {
        x: p ? p.x : cx() + (Math.random() - 0.5) * 120,
        y: p ? p.y : cy() + (Math.random() - 0.5) * 120,
        vx: 0, vy: 0,
      });
    });
    edges = g.edges || [];
    setupScrubber();
    alpha = 1;
    selected = null;
    $("graph-node-card").hidden = true;
    kick();
  }

  function nodeAt(slug) { return nodes.find((n) => n.slug === slug); }
  function visible(n) { return !(n.first_seen && n.first_seen > timeCut); }

  // ---- physics ----
  function step() {
    const REP = 2600, SPRING = 0.02, REST = 78, CENTER = 0.015, DAMP = 0.86;
    for (const a of nodes) {
      if (a === dragging) continue;
      let fx = 0, fy = 0;
      for (const b of nodes) {
        if (a === b) continue;
        let dx = a.x - b.x, dy = a.y - b.y;
        let d2 = dx * dx + dy * dy || 0.01;
        const f = REP / d2;
        const d = Math.sqrt(d2);
        fx += (dx / d) * f; fy += (dy / d) * f;
      }
      // centering — the focus is pulled harder so it settles in the middle
      const cf = a.slug === focus ? CENTER * 8 : CENTER;
      fx += (cx() - a.x) * cf; fy += (cy() - a.y) * cf;
      a.fx = fx; a.fy = fy;
    }
    for (const e of edges) {
      const a = nodeAt(e.src), b = nodeAt(e.dst);
      if (!a || !b) continue;
      let dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.sqrt(dx * dx + dy * dy) || 0.01;
      // similarity springs are slack, so lens edges do not distort real structure
      const k = e.provenance === "similarity" ? SPRING * 0.3 : SPRING;
      const f = (d - REST) * k;
      const ux = dx / d, uy = dy / d;
      if (a !== dragging) { a.fx += ux * f; a.fy += uy * f; }
      if (b !== dragging) { b.fx -= ux * f; b.fy -= uy * f; }
    }
    for (const a of nodes) {
      if (a === dragging) continue;
      a.vx = (a.vx + a.fx * alpha) * DAMP;
      a.vy = (a.vy + a.fy * alpha) * DAMP;
      a.x += a.vx; a.y += a.vy;
    }
    alpha *= 0.985;
  }

  // ---- render ----
  function draw() {
    const t = theme();
    const w = canvas.clientWidth, h = canvas.clientHeight;
    ctx.clearRect(0, 0, w, h);

    for (const e of edges) {
      const a = nodeAt(e.src), b = nodeAt(e.dst);
      if (!a || !b || !visible(a) || !visible(b)) continue;
      styleEdge(e, a === hovered || b === hovered || a === selected || b === selected, t);
      ctx.beginPath();
      ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
      ctx.setLineDash([]);
    }

    for (const n of nodes) {
      if (!visible(n)) continue;
      const r = radius(n);
      const dim = n.hops >= 2 ? 0.5 : 1; // ego dimming: outer rings recede
      ctx.globalAlpha = dim;
      ctx.beginPath();
      ctx.arc(n.x, n.y, r, 0, Math.PI * 2);
      ctx.fillStyle = color(n.kind, t);
      ctx.fill();
      if (n === selected || n === hovered || n.slug === focus) {
        // The selection ring is the one thing on this canvas that is "live" —
        // it gets the same accent the rest of the app uses for that.
        ctx.lineWidth = 2;
        ctx.strokeStyle = t.patina;
        ctx.stroke();
      }
      ctx.globalAlpha = 1;
      // label the focus, selection, hover, and 1-hop hubs
      if (n.slug === focus || n === selected || n === hovered || (n.hops <= 1 && n.degree >= 2)) {
        ctx.fillStyle = t.ink;
        ctx.font = "600 11px " + t.font;
        ctx.textAlign = "center";
        ctx.fillText(n.title || n.slug, n.x, n.y + r + 12);
      }
    }
  }

  function styleEdge(e, active, t) {
    ctx.lineWidth = active ? 2 : 1;
    if (e.provenance === "wikilink") {
      ctx.strokeStyle = mix(t.ink3, active ? 0.9 : 0.55);
      ctx.setLineDash([]);
    } else if (e.provenance === "typed") {
      const solid = e.conf >= 0.8;
      ctx.strokeStyle = mix(t.patina, (active ? 0.9 : 0.55) * Math.max(0.4, e.conf));
      ctx.setLineDash(solid ? [] : [3, 3]);
    } else {
      // similarity: faint dotted lens
      ctx.strokeStyle = mix(t.patina, active ? 0.35 : 0.15);
      ctx.setLineDash([1, 4]);
    }
  }

  // mix applies an alpha to a CSS color string by wrapping it in color-mix(),
  // which every engine that understands oklch() also understands — so this
  // works whatever color space the token happens to be defined in.
  function mix(cssColor, alpha) {
    return `color-mix(in oklch, ${cssColor} ${Math.round(alpha * 100)}%, transparent)`;
  }

  function frame() {
    step();
    draw();
    if (alpha > 0.008 || dragging) raf = requestAnimationFrame(frame);
    else raf = null;
  }
  function kick() { alpha = Math.max(alpha, 0.4); if (!raf) raf = requestAnimationFrame(frame); }

  // ---- interaction ----
  function pick(mx, my) {
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i];
      if (!visible(n)) continue;
      const dx = mx - n.x, dy = my - n.y;
      if (dx * dx + dy * dy <= (radius(n) + 4) ** 2) return n;
    }
    return null;
  }

  function onMove(ev) {
    const rect = canvas.getBoundingClientRect();
    const mx = ev.clientX - rect.left, my = ev.clientY - rect.top;
    if (dragging) { dragging.x = mx; dragging.y = my; kick(); return; }
    const n = pick(mx, my);
    if (n !== hovered) { hovered = n; showTip(n, ev); kick(); }
  }
  function onDown(ev) {
    const rect = canvas.getBoundingClientRect();
    const n = pick(ev.clientX - rect.left, ev.clientY - rect.top);
    if (n) { dragging = n; select(n); }
  }
  function onUp() { dragging = null; }
  function onDblClick(ev) {
    const rect = canvas.getBoundingClientRect();
    const n = pick(ev.clientX - rect.left, ev.clientY - rect.top);
    if (n && n.kind !== "missing") load(n.slug); // re-centre the ego graph
  }

  function select(n) {
    selected = n;
    const card = $("graph-node-card");
    if (!n) { card.hidden = true; return; }
    $("gn-title").textContent = n.title || n.slug;
    $("gn-meta").textContent = `${n.kind} · degree ${n.degree} · ${n.slug}`;
    $("gn-obsidian").onclick = () => window.go.main.App.OpenInObsidian(n.slug);
    $("gn-obsidian").style.display = n.kind === "missing" ? "none" : "";
    card.hidden = false;
  }

  function showTip(n, ev) {
    if (tip) { tip.remove(); tip = null; }
    if (!n) return;
    tip = document.createElement("div");
    tip.className = "graph-tip";
    tip.innerHTML = `${n.title || n.slug}<br><span class="k">${n.kind} · deg ${n.degree}${n.hops ? " · " + n.hops + " hop" : ""}</span>`;
    document.body.append(tip);
    const rect = canvas.getBoundingClientRect();
    tip.style.left = rect.left + n.x + 12 + "px";
    tip.style.top = rect.top + n.y - 8 + "px";
  }

  // ---- time scrubber ----
  function setupScrubber() {
    const seen = nodes.map((n) => n.first_seen).filter((t) => t > 0);
    const scrub = $("graph-scrub");
    if (seen.length < 2) { scrub.hidden = true; timeCut = Infinity; return; }
    scrub.hidden = false;
    const min = Math.min(...seen), max = Math.max(...seen);
    const slider = $("graph-time");
    slider.oninput = () => {
      const frac = slider.value / 100;
      timeCut = frac >= 1 ? Infinity : min + (max - min) * frac;
      $("graph-scrub-label").textContent = frac >= 1 ? "now" :
        new Date(timeCut * 1000).toISOString().slice(0, 10);
      kick();
    };
    slider.value = 100; timeCut = Infinity;
    $("graph-scrub-label").textContent = "now";
  }

  // ---- sizing ----
  function resize() {
    dpr = window.devicePixelRatio || 1;
    canvas.width = canvas.clientWidth * dpr;
    canvas.height = canvas.clientHeight * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    kick();
  }

  function init() {
    canvas = $("graph-canvas");
    ctx = canvas.getContext("2d");
    canvas.addEventListener("mousemove", onMove);
    canvas.addEventListener("mousedown", onDown);
    window.addEventListener("mouseup", onUp);
    canvas.addEventListener("dblclick", onDblClick);
    canvas.addEventListener("mouseleave", () => { if (tip) { tip.remove(); tip = null; } hovered = null; });

    $("graph-sim").addEventListener("change", (e) => { similarity = e.target.checked; load(focus); });
    document.querySelectorAll(".hopbtn").forEach((b) =>
      b.addEventListener("click", () => {
        document.querySelectorAll(".hopbtn").forEach((x) => x.classList.remove("on"));
        b.classList.add("on");
        hops = +b.dataset.hops;
        load(focus);
      }));
    $("graph-search").addEventListener("keydown", async (e) => {
      if (e.key !== "Enter") return;
      const slug = await window.go.main.App.GraphFind(e.target.value.trim());
      if (slug) { load(slug); e.target.value = ""; }
    });
    new ResizeObserver(resize).observe(canvas);
  }

  // Public: called when the Graph tab is shown.
  async function open() {
    if (!ctx) init();
    resize();
    if (!nodes.length) await load("");
    else kick();
  }

  return { open, init };
})();
