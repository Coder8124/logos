// background.js — the only file that talks to the local brain process.
//
// A Manifest V3 service worker is killed and restarted between events, so it
// cannot hold a long-lived WebSocket the way a persistent background page
// could — the connection would silently die minutes into a chat session with
// nothing to notice. Instead every call opens its own short-lived socket,
// does one initialize + the requested call, and closes. Slower per call, but
// there is nothing to keep alive and nothing that can go stale.

const DEFAULTS = { port: 8137, token: "" };

async function getConfig() {
  const stored = await chrome.storage.local.get(DEFAULTS);
  return stored;
}

// One WebSocket round trip: connect, initialize, send the real request,
// return its response, close. Rejects on any transport error so the caller
// (the content script) can show the user *why* — "no pairing token" and
// "brain isn't running" are different problems and should read as different
// errors, not a generic "something went wrong".
function callBrain(method, params) {
  return new Promise(async (resolve, reject) => {
    const { port, token } = await getConfig();
    if (!token) {
      reject(new Error("not paired yet — set the pairing token in the extension's options page"));
      return;
    }
    const url = `ws://127.0.0.1:${port}/mcp?token=${encodeURIComponent(token)}`;
    let ws;
    try {
      ws = new WebSocket(url);
    } catch (e) {
      reject(new Error(`could not open a connection to brain: ${e.message}`));
      return;
    }

    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error("brain did not respond in time — is `brain mcp serve --http` running?"));
    }, 10000);

    let initialized = false;
    let nextId = 1;

    ws.onerror = () => {
      clearTimeout(timeout);
      reject(new Error("could not reach brain on 127.0.0.1 — is `brain mcp serve --http` running, and does the pairing token match?"));
    };

    ws.onopen = () => {
      ws.send(JSON.stringify({
        jsonrpc: "2.0", id: nextId++, method: "initialize",
        params: { protocolVersion: "2024-11-05" },
      }));
    };

    ws.onmessage = (ev) => {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch {
        return;
      }
      if (!initialized) {
        initialized = true;
        if (msg.error) {
          clearTimeout(timeout);
          ws.close();
          reject(new Error(`brain rejected the handshake: ${msg.error.message || JSON.stringify(msg.error)}`));
          return;
        }
        ws.send(JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }));
        return;
      }
      clearTimeout(timeout);
      ws.close();
      if (msg.error) {
        reject(new Error(msg.error.message || JSON.stringify(msg.error)));
      } else {
        resolve(msg.result);
      }
    };
  });
}

// The content script never touches the socket directly — it only knows how
// to render a panel and read the page. Routing every call through here keeps
// the pairing token out of the page's own JS context, which a hostile script
// on chatgpt.com could otherwise read.
chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.type !== "brain-call") return;
  callBrain(msg.method, msg.params || {})
    .then((result) => sendResponse({ ok: true, result }))
    .catch((err) => sendResponse({ ok: false, error: String(err.message || err) }));
  return true; // keep the message channel open for the async response
});
