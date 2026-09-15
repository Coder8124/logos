// content-chatgpt.js — a panel injected into chatgpt.com that lets the user
// pull from, or write to, logos's local memory without leaving the chat.
//
// Scope, stated plainly (see extension/README.md): chatgpt.com's web UI does
// not expose a tool-calling hook to third-party extensions, and the
// alternative — scraping the model's streamed response for a hand-rolled
// "tool call" syntax — is exactly the DOM/prompt-engineering fragility the
// 0.4.5 plan called out as the wrong thing to build first. So v1 is
// user-triggered: a panel lists logos's tools, the user picks one and fills
// its arguments, the result is inserted into the compose box as text. That is
// slower than a model calling tools on its own, but it is real today and
// breaks in an obvious way (a selector stops matching) rather than a subtle
// one (a parser silently misreads a tool call).

(function () {
  const PANEL_ID = "logos-bridge-panel";
  if (document.getElementById(PANEL_ID)) return; // don't double-inject on SPA navigation

  function callLogos(method, params) {
    return new Promise((resolve, reject) => {
      chrome.runtime.sendMessage({ type: "logos-call", method, params }, (resp) => {
        if (chrome.runtime.lastError) {
          reject(new Error(chrome.runtime.lastError.message));
        } else if (!resp || !resp.ok) {
          reject(new Error(resp ? resp.error : "no response from extension background"));
        } else {
          resolve(resp.result);
        }
      });
    });
  }

  // ChatGPT's compose box has changed markup before and will again; this is
  // the one place that breaks first if it does. It's isolated to this single
  // function on purpose, so a selector fix touches one line, not the panel.
  function findComposeBox() {
    return (
      document.querySelector("#prompt-textarea") ||
      document.querySelector('div[contenteditable="true"]') ||
      document.querySelector("textarea")
    );
  }

  function insertText(text) {
    const box = findComposeBox();
    if (!box) {
      alert("logos: couldn't find the ChatGPT compose box — paste manually:\n\n" + text);
      return;
    }
    box.focus();
    if (box.tagName === "TEXTAREA") {
      const start = box.selectionStart ?? box.value.length;
      box.value = box.value.slice(0, start) + text + box.value.slice(start);
      box.dispatchEvent(new Event("input", { bubbles: true }));
    } else {
      // contenteditable: execCommand is deprecated but still the one thing
      // that makes React's own input handlers notice the change; a direct
      // textContent write is invisible to ChatGPT's send button state.
      document.execCommand("insertText", false, text);
    }
  }

  function toolResultToText(toolName, result) {
    const content = result?.content;
    if (Array.isArray(content)) {
      return content.map((c) => c.text || "").join("\n");
    }
    return JSON.stringify(result, null, 2);
  }

  const panel = document.createElement("div");
  panel.id = PANEL_ID;
  panel.style.cssText = `
    position: fixed; bottom: 16px; right: 16px; z-index: 999999;
    font: 13px system-ui, sans-serif; background: #fff; color: #111;
    border: 1px solid #ccc; border-radius: 8px; box-shadow: 0 2px 12px rgba(0,0,0,.2);
    width: 280px; max-height: 60vh; overflow: auto; display: none;
  `;
  const toggle = document.createElement("button");
  toggle.textContent = "🧠";
  toggle.title = "logos memory bridge";
  toggle.style.cssText = `
    position: fixed; bottom: 16px; right: 16px; z-index: 1000000;
    width: 36px; height: 36px; border-radius: 18px; border: none;
    background: #222; color: #fff; font-size: 16px; cursor: pointer;
  `;
  // The panel is built from nodes, never from markup strings: error text and
  // tool descriptions come from the bridge, and anything that ever echoes
  // vault content must not become markup inside chatgpt.com.
  function el(tag, style, text) {
    const node = document.createElement(tag);
    if (style) node.style.cssText = style;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function renderStatus(...children) {
    const box = el("div", "padding:12px");
    box.append(...children);
    panel.replaceChildren(box);
  }

  async function renderTools() {
    renderStatus("loading logos's tools…");
    let tools;
    try {
      const result = await callLogos("tools/list", {});
      tools = result.tools || [];
    } catch (e) {
      const open = el("a", "", "open pairing settings");
      open.href = "#";
      open.addEventListener("click", (ev) => {
        ev.preventDefault();
        window.open(chrome.runtime.getURL("options.html"));
      });
      renderStatus(el("b", "", "not connected"), el("br"), e.message, el("br"), el("br"), open);
      return;
    }

    // recall and remember cover the everyday case (pull context in, save a
    // fact out); the rest of the tool set is one click further so the panel
    // isn't a wall of buttons for tools most sessions never touch.
    const primary = ["recall", "remember"];
    const rows = tools
      .sort((a, b) => (primary.includes(b.name) ? 1 : 0) - (primary.includes(a.name) ? 1 : 0))
      .map((t) => {
        const row = el("div", "border-bottom:1px solid #eee;padding:8px 12px");
        const run = el("button", "", "run");
        run.dataset.tool = t.name;
        run.className = "logos-run";
        row.append(
          el("div", "font-weight:600", t.name),
          el("div", "color:#666;font-size:12px;margin:2px 0 6px", `${(t.description || "").split(".")[0]}.`),
          run
        );
        return row;
      });
    panel.replaceChildren(
      el("div", "padding:8px 12px;font-weight:700;border-bottom:1px solid #ddd", "logos memory"),
      ...rows
    );

    panel.querySelectorAll(".logos-run").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const name = btn.dataset.tool;
        const tool = tools.find((t) => t.name === name);
        const params = {};
        const props = tool.inputSchema?.properties || {};
        for (const [key, schema] of Object.entries(props)) {
          const required = (tool.inputSchema?.required || []).includes(key);
          const val = prompt(`${name}.${key}${required ? " (required)" : " (optional)"}\n${schema.description || ""}`);
          if (val === null) return; // cancelled
          if (val.trim() === "") continue;
          params[key] = schema.type === "integer" ? parseInt(val, 10) : schema.type === "boolean" ? val === "true" : val;
        }
        btn.textContent = "running…";
        btn.disabled = true;
        try {
          const result = await callLogos("tools/call", { name, arguments: params });
          insertText(toolResultToText(name, result));
        } catch (e) {
          alert(`logos: ${e.message}`);
        } finally {
          btn.textContent = "run";
          btn.disabled = false;
        }
      });
    });
  }

  document.body.appendChild(toggle);
  document.body.appendChild(panel);
  toggle.addEventListener("click", () => {
    const opening = panel.style.display === "none";
    panel.style.display = opening ? "block" : "none";
    if (opening) renderTools();
  });
})();
