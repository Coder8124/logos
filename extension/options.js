const portEl = document.getElementById("port");
const tokenEl = document.getElementById("token");
const statusEl = document.getElementById("status");

async function load() {
  const { port, token } = await chrome.storage.local.get({ port: 8137, token: "" });
  portEl.value = port;
  tokenEl.value = token;
}

function setStatus(text, ok) {
  statusEl.textContent = text;
  statusEl.className = ok ? "ok" : "err";
}

document.getElementById("save").addEventListener("click", async () => {
  const port = parseInt(portEl.value, 10) || 8137;
  const token = tokenEl.value.trim();
  await chrome.storage.local.set({ port, token });
  setStatus("saved.", true);
});

// Round-trips through background.js exactly like a content script would —
// this is the same code path a real tool call takes, so "test connection"
// passing means pairing actually works, not just that the form saved.
document.getElementById("test").addEventListener("click", async () => {
  setStatus("connecting…", true);
  await chrome.storage.local.set({
    port: parseInt(portEl.value, 10) || 8137,
    token: tokenEl.value.trim(),
  });
  chrome.runtime.sendMessage({ type: "brain-call", method: "tools/list", params: {} }, (resp) => {
    if (!resp || !resp.ok) {
      setStatus(`failed: ${resp ? resp.error : "no response"}`, false);
      return;
    }
    const n = (resp.result?.tools || []).length;
    setStatus(`connected — brain reports ${n} tools.`, true);
  });
});

load();
