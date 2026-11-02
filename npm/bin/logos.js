#!/usr/bin/env node
"use strict";

// The launcher. It finds the prebuilt binary for this platform and becomes it.
//
// Why a wrapper at all: npm cannot ship one package containing five platforms'
// binaries without every user downloading all five. So the real binaries live in
// per-platform packages gated by `os`/`cpu`, npm installs only the matching one,
// and this file resolves it.
//
// The important constraint is that `logos mcp serve` speaks newline-delimited
// JSON-RPC over stdin/stdout. Anything this wrapper writes to stdout corrupts
// that stream, and any buffering between the host and the binary risks stalling
// it. So stdio is inherited — the child gets the real file descriptors and this
// process is not in the data path at all — and every diagnostic goes to stderr.

const { spawn } = require("child_process");

// npm's platform vocabulary, which is Node's, not Go's.
const PLATFORMS = {
  "darwin arm64": "@noeton/logos-darwin-arm64",
  "darwin x64": "@noeton/logos-darwin-x64",
  "linux x64": "@noeton/logos-linux-x64",
  "linux arm64": "@noeton/logos-linux-arm64",
  "win32 x64": "@noeton/logos-win32-x64",
};

function binaryPath() {
  const key = `${process.platform} ${process.arch}`;
  const pkg = PLATFORMS[key];
  if (!pkg) {
    fail(
      `no prebuilt binary for ${key}.`,
      "",
      "Supported: " + Object.keys(PLATFORMS).join(", ") + ".",
      "Build from source instead:",
      "  go install github.com/Coder8124/logos/cmd/logos@latest"
    );
  }

  const exe = process.platform === "win32" ? "logos.exe" : "logos";
  try {
    // Resolve through the package's own entry so npm/pnpm/yarn layouts, symlinks
    // and nested node_modules all work without guessing at directory structure.
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch (e) {
    fail(
      `installed, but the binary package for ${key} is missing.`,
      "",
      `Expected: ${pkg}`,
      "",
      "This usually means optional dependencies were skipped. Try:",
      "  npm install --include=optional",
      "",
      "If you installed with --no-optional or a lockfile from another platform,",
      "reinstall without it. Failing that, install directly:",
      `  npm install ${pkg}`
    );
  }
}

function fail(...lines) {
  for (const line of lines) console.error(line ? `logos: ${line}` : "");
  process.exit(1);
}

// Inheriting stdio means the JSON-RPC stream flows through the real descriptors
// with no Node layer in between; this process only waits. It waits with spawn,
// not spawnSync, because a blocked event loop cannot handle a signal: a SIGTERM
// meant for the server killed this wrapper alone, and the binary ran on as an
// orphan for as long as anyone held its stdin.
const child = spawn(binaryPath(), process.argv.slice(2), {
  stdio: "inherit",
  windowsHide: true,
});

// At a terminal, Ctrl-C reaches the whole foreground group, the binary
// included, and the binary decides what it means: `logos resume` stays up while
// an agent it started handles the key. Forwarding it as well delivered it twice,
// and this process dying under it handed the shell back while the agent still
// owned the terminal, so there it is only held. Off a terminal a signal is aimed
// at this process alone, and a host stopping its server with SIGINT must still
// stop it, so there it is forwarded like the others.
const atTerminal = require("tty").isatty(0);
for (const sig of ["SIGTERM", "SIGHUP", "SIGINT"]) {
  if (sig === "SIGINT" && atTerminal) {
    process.on(sig, () => {});
    continue;
  }
  process.on(sig, () => child.kill(sig));
}

child.on("error", (err) => {
  if (err.code === "EACCES") {
    fail(
      "the binary is not executable.",
      "",
      "Reinstall to restore its permissions:",
      "  npm install --force @noeton/logos"
    );
  }
  fail(`could not start the binary: ${err.message}`);
});

// A child killed by a signal has a null status. Reproduce the signal rather
// than inventing an exit code, so `logos mcp serve` under a host that kills its
// servers reports what actually happened. The forwarding handlers come off
// first, or the signal would be caught here again instead of ending this process.
child.on("exit", (status, signal) => {
  if (signal) {
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(status === null ? 1 : status);
});
