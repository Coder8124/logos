// Theme: auto / dark / light. Self-contained and dependency-free, like app.js:
// it wires the header's existing theme button, applies the chosen palette by
// setting data-theme on <html>, and persists the choice. "Auto" tracks the
// time of day and flips between light and dark on its own. All theming lives
// in CSS custom properties, so this only ever swaps one attribute.

(function () {
  var KEY = "brain.theme";
  var MODES = ["auto", "dark", "light"];
  var ICON = { auto: "◐", dark: "●", light: "○" };

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
    var btn = document.getElementById("theme-btn");
    if (btn) {
      btn.textContent = ICON[mode] || ICON.dark;
      btn.title = "Theme: " + mode + " (click to change)";
    }
  }

  function cycle() {
    var mode = getMode();
    var next = MODES[(MODES.indexOf(mode) + 1) % MODES.length];
    setMode(next);
    apply(next);
  }

  function wire() {
    var btn = document.getElementById("theme-btn");
    if (btn) btn.addEventListener("click", cycle);
  }

  // Keep Auto honest: re-resolve on an interval and whenever the window
  // regains focus, so the theme flips at dawn/dusk without a reload.
  function watchAuto() {
    function tick() { if (getMode() === "auto") apply("auto"); }
    setInterval(tick, 60 * 1000);
    window.addEventListener("focus", tick);
    document.addEventListener("visibilitychange", function () { if (!document.hidden) tick(); });
  }

  function init() {
    apply(getMode()); // the head bootstrap already set data-theme; this syncs the button
    wire();
    watchAuto();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
