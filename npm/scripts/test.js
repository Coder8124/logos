#!/usr/bin/env node
"use strict";

// Proves the wrapper works before anything is published.
//
// The risk with putting Node in front of an MCP server is the stdio path: if
// the wrapper buffers, reorders, or writes a single stray byte to stdout, the
// JSON-RPC stream is corrupt and the host reports an unhelpful protocol error.
// So this does not just check that the binary runs — it drives a real
// initialize handshake through the wrapper and asserts stdout carries exactly
// the protocol and nothing else.

const fs = require("fs");
const path = require("path");
const { spawnSync, spawn } = require("child_process");

const root = path.join(__dirname, "..");
const launcher = path.join(root, "bin", "logos.js");

const KEY = { darwin: "darwin", linux: "linux", win32: "win32" }[process.platform];
const ARCH = { arm64: "arm64", x64: "x64" }[process.arch];
if (!KEY || !ARCH) {
  console.error(`test: unsupported host ${process.platform} ${process.arch}`);
  process.exit(1);
}
const plat = `${KEY}-${ARCH}`;
const exe = process.platform === "win32" ? "brain.exe" : "brain";
const platDir = path.join(root, "platforms", plat);

if (!fs.existsSync(path.join(platDir, "bin", exe))) {
  console.error(`test: no built package for ${plat} — run: node scripts/build.js`);
  process.exit(1);
}

// Stand in for what npm does at install time, so require.resolve in the
// launcher finds the platform package exactly as it would for a real user.
const link = path.join(root, "node_modules", "@brainyprime", `logos-${plat}`);
fs.mkdirSync(path.dirname(link), { recursive: true });
fs.rmSync(link, { recursive: true, force: true });
fs.symlinkSync(platDir, link, "junction");

let failed = 0;
function check(name, ok, detail) {
  console.log(`  ${ok ? "ok  " : "FAIL"}  ${name}`);
  if (!ok) {
    failed++;
    if (detail) console.log(`        ${String(detail).split("\n").join("\n        ")}`);
  }
}

// 1. The launcher resolves and runs the binary, and the exit code survives.
//    The binary answers with its own name, `brain`, not the package's — that is
//    the seam this wrapper exists to bridge, so asserting on it is deliberate.
const v = spawnSync(process.execPath, [launcher, "version"], { encoding: "utf8" });
check("logos version runs through the wrapper", v.status === 0 && /^brain /.test(v.stdout || ""), v.stderr || v.stdout);

// 2. A non-zero exit from the binary reaches the caller, rather than being
//    swallowed into a success the host would misread as a clean shutdown.
const bad = spawnSync(process.execPath, [launcher, "definitely-not-a-command"], { encoding: "utf8" });
check("a failing command exits non-zero", bad.status !== 0, `status=${bad.status}`);

// 3. The MCP handshake, end to end through the wrapper. This is the one that
//    matters: it is how every host will actually invoke this package.
const mcp = spawn(process.execPath, [launcher, "mcp", "serve"], {
  env: { ...process.env, BRAIN_VAULT: fs.mkdtempSync(path.join(require("os").tmpdir(), "brain-npm-vault-")) },
});
let stdout = "";
mcp.stdout.on("data", (d) => (stdout += d));
mcp.stdin.write(
  JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "initialize",
    params: { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "npm-test", version: "0" } },
  }) + "\n"
);

setTimeout(() => {
  mcp.stdin.end();
  mcp.kill();

  const lines = stdout.split("\n").filter((l) => l.trim() !== "");
  check("mcp serve answered initialize", lines.length > 0, "no output on stdout");

  let clean = lines.length > 0;
  let why = "";
  for (const line of lines) {
    try {
      const msg = JSON.parse(line);
      if (msg.jsonrpc !== "2.0") {
        clean = false;
        why = `non-JSON-RPC object on stdout: ${line.slice(0, 120)}`;
      }
    } catch (e) {
      // The failure mode this test exists for: anything non-protocol on stdout.
      clean = false;
      why = `non-JSON on stdout, which corrupts the stream: ${line.slice(0, 120)}`;
    }
  }
  check("stdout carries only JSON-RPC", clean, why);

  const first = lines[0] ? JSON.parse(lines[0]) : {};
  check(
    "initialize returned a result",
    first.result && first.result.protocolVersion === "2024-11-05",
    JSON.stringify(first).slice(0, 200)
  );

  console.log(failed === 0 ? "\nall checks passed" : `\n${failed} check(s) failed`);
  process.exit(failed === 0 ? 0 : 1);
}, 3000);
