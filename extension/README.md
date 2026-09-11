# Logos Bridge (browser extension)

Connects brain's local memory to chatgpt.com's compose box. Local only —
the extension talks to `127.0.0.1` and nowhere else; see
`internal/mcpserver/http.go` for the two gates (pairing token + Origin
allowlist) that keep any other open tab from reaching it.

## Scope, stated plainly

chatgpt.com does not give third-party extensions a hook to let the model call
tools directly. The other way to get that — scrape the model's streamed
response for a hand-rolled "tool call" block — is DOM/prompt-engineering
against a UI that changes without notice, and was deliberately not the
starting point.

So v1 is user-triggered: a small panel lists brain's tools (`recall`,
`remember`, and the rest), the user picks one, fills its arguments, and the
result is inserted into the compose box as text. Slower than automatic
tool-calling, but it fails obviously (a selector stops matching, the panel
says so) rather than silently (a parser misreads a tool call and inserts
garbage). Claude.ai and Perplexity content scripts, and a more automatic
tool-call flow for any of the three, are follow-on work, not part of this cut.

## Why `host_permissions` says `http://`, not `ws://`

The extension only ever opens a WebSocket (`ws://127.0.0.1:<port>/mcp`), so
`"ws://127.0.0.1/*"` looks like the permission it should be asking for. It is
not: MV3 match patterns accept `http`, `https`, `file`, `ftp`, `urn` and `*`
only, and Chrome rejects a `ws://` entry with an "Invalid value for
'host_permissions'" warning at load. The `http://127.0.0.1/*` entry is what
covers the loopback origin; the handshake is an HTTP upgrade against it.

## Setup

1. Build and run brain's web bridge:
   ```sh
   export BRAIN_BRIDGE_ORIGIN="chrome-extension://<extension-id>"
   brain mcp serve --http --port 8137
   ```
   It prints a pairing token on startup (also visible any time via
   `.brain/webbridge.json` in the vault, or `brain doctor`'s paired/not-paired
   line).
2. Load the extension unpacked: `chrome://extensions` → Developer mode →
   "Load unpacked" → select this `extension/` directory. Chrome shows the
   extension's id there — that's the `<extension-id>` above. (Note the
   ordering: the id is assigned when you load it, but the server needs it at
   `BRAIN_BRIDGE_ORIGIN` before you connect — load the extension once first
   to learn the id, then start the server with it.)
3. Open the extension's options page (right-click its toolbar icon →
   Options, or `chrome://extensions` → Details → Extension options). Enter
   the port (8137) and the pairing token from step 1. Click "Test connection"
   — it should report the number of tools brain exposes.
4. Open chatgpt.com. A small 🧠 button appears bottom-right. Click it to open
   the panel, pick a tool, fill its arguments, and the result lands in the
   compose box.

## Manual test procedure (no headless harness — chatgpt.com's live DOM has no
place in this repo's CI)

- With the server not running: click 🧠 → panel should say "not connected"
  with a reason, not silently show nothing.
- With the server running but the wrong token saved: "Test connection" in
  options should fail with a token/rejection message, not hang.
- With everything correct: "Test connection" reports the tool count; open the
  panel on chatgpt.com, run `recall` with a query that matches something
  stored, confirm the result text appears in the compose box.
- Run `remember` with a short fact, confirm via `brain memory log` on the
  vault that it landed, confirm the panel's result text (the confirmation
  message) appears in the compose box.
- Reload chatgpt.com (SPA navigation) and confirm the panel re-injects rather
  than duplicating (`content-chatgpt.js`'s guard on `PANEL_ID`).

Record any chatgpt.com DOM-selector break by fixing `findComposeBox()` in
`content-chatgpt.js` — it's isolated there on purpose.
