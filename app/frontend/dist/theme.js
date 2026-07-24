// Visual themes. Self-contained and dependency-free, like app.js: it injects a
// picker into the header, applies the chosen palette by setting data-theme on
// <html>, and persists the choice. "Auto" tracks the time of day and flips
// between light and dark on its own. All theming lives in CSS variables, so this
// only ever swaps one attribute.

(function () {
  var KEY = "brain.theme";

  // Each option: the stored mode, a label, and a two-stop swatch (bg → accent)
  // so the menu previews the palette at a glance.
  var THEMES = [
    { mode: "auto",    label: "Auto",    sw: ["#12141c", "#ffffff"] },
    { mode: "dark",    label: "Dark",    sw: ["#12141c", "#6b78e8"] },
    { mode: "light",   label: "Light",   sw: ["#ffffff", "#5561d6"] },
    { mode: "paper",   label: "Paper",   sw: ["#f7f0e1", "#a8652f"] },
    { mode: "digital", label: "Digital", sw: ["#0a0f0a", "#22e56a"] },
    { mode: "blue",    label: "Blue",    sw: ["#0d1a2b", "#3b9dff"] },
    { mode: "red",     label: "Red",     sw: ["#1e0f12", "#ef4d5a"] },
  ];

  // getMode / setMode persist the user's choice (the mode, not the resolved
  // palette — so "auto" stays auto across launches).
  function getMode() {
    try { return localStorage.getItem(KEY) || "dark"; } catch (e) { return "dark"; }
  }
  function setMode(m) {
    try { localStorage.setItem(KEY, m); } catch (e) {}
  }

  // resolve turns a mode into an actual palette. Auto is light through the day
  // (07:00–18:59) and dark at night.
  function resolve(mode) {
    if (mode !== "auto") return mode;
    var h = new Date().getHours();
    return (h >= 7 && h < 19) ? "light" : "dark";
  }

  function apply(mode) {
    document.documentElement.setAttribute("data-theme", resolve(mode));
    markChecked(mode);
  }

  var menu; // built lazily
  function markChecked(mode) {
    if (!menu) return;
    menu.querySelectorAll(".theme-opt").forEach(function (opt) {
      opt.setAttribute("aria-checked", String(opt.dataset.mode === mode));
    });
  }

  function build() {
    var header = document.querySelector("header");
    if (!header) return;

    var wrap = document.createElement("div");
    wrap.className = "theme";

    var btn = document.createElement("button");
    btn.className = "theme-btn";
    btn.type = "button";
    btn.title = "Theme";
    btn.textContent = "◐";
    btn.setAttribute("aria-haspopup", "true");
    btn.setAttribute("aria-expanded", "false");

    menu = document.createElement("div");
    menu.className = "theme-menu";
    menu.setAttribute("role", "menu");

    THEMES.forEach(function (t, i) {
      // A separator after Auto sets the automatic mode apart from the fixed ones.
      if (i === 1) menu.appendChild(sep());
      var opt = document.createElement("button");
      opt.className = "theme-opt";
      opt.type = "button";
      opt.dataset.mode = t.mode;
      opt.setAttribute("role", "menuitemradio");

      var sw = document.createElement("span");
      sw.className = "tk-sw";
      sw.style.background = "linear-gradient(135deg, " + t.sw[0] + " 0 50%, " + t.sw[1] + " 50% 100%)";

      var name = document.createElement("span");
      name.textContent = t.label;

      var check = document.createElement("span");
      check.className = "tk-check";
      check.textContent = "✓";

      opt.appendChild(sw);
      opt.appendChild(name);
      opt.appendChild(check);
      opt.addEventListener("click", function () {
        setMode(t.mode);
        apply(t.mode);
        close();
      });
      menu.appendChild(opt);
    });

    function open() { menu.classList.add("open"); btn.setAttribute("aria-expanded", "true"); }
    function close() { menu.classList.remove("open"); btn.setAttribute("aria-expanded", "false"); }
    btn.addEventListener("click", function (e) {
      e.stopPropagation();
      menu.classList.contains("open") ? close() : open();
    });
    document.addEventListener("click", function (e) { if (!wrap.contains(e.target)) close(); });
    document.addEventListener("keydown", function (e) { if (e.key === "Escape") close(); });

    wrap.appendChild(btn);
    wrap.appendChild(menu);

    // Place the control just before the runtime label on the right.
    var runtime = document.getElementById("runtime");
    if (runtime && runtime.parentNode === header) header.insertBefore(wrap, runtime);
    else header.appendChild(wrap);

    markChecked(getMode());
  }

  function sep() {
    var s = document.createElement("div");
    s.className = "theme-sep";
    return s;
  }

  // Keep Auto honest: re-resolve on an interval and whenever the window regains
  // focus, so the theme flips at dawn/dusk without a reload.
  function watchAuto() {
    function tick() { if (getMode() === "auto") apply("auto"); }
    setInterval(tick, 60 * 1000);
    window.addEventListener("focus", tick);
    document.addEventListener("visibilitychange", function () { if (!document.hidden) tick(); });
  }

  function init() {
    apply(getMode()); // the head bootstrap already set it; this syncs the menu state
    build();
    watchAuto();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
