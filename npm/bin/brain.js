#!/usr/bin/env node
"use strict";

// The launcher. It finds the prebuilt binary for this platform and becomes it.
//
// Why a wrapper at all: npm cannot ship one package containing five platforms'
// binaries without every user downloading all five. So the real binaries live in
// per-platform packages gated by `os`/`cpu`, npm installs only the matching one,
// and this file resolves it.
//
// The important constraint is that `brain mcp serve` speaks newline-delimited
// JSON-RPC over stdin/stdout. Anything this wrapper writes to stdout corrupts
// that stream, and any buffering between the host and the binary risks stalling
// it. So stdio is inherited — the child gets the real file descriptors and this
// process is not in the data path at all — and every diagnostic goes to stderr.

const { spawnSync } = require("child_process");

// npm's platform vocabulary, which is Node's, not Go's.
const PLATFORMS = {
  "darwin arm64": "@brainyprime/brain-darwin-arm64",
  "darwin x64": "@brainyprime/brain-darwin-x64",
  "linux x64": "@brainyprime/brain-linux-x64",
  "linux arm64": "@brainyprime/brain-linux-arm64",
  "win32 x64": "@brainyprime/brain-win32-x64",
};

function binaryPath() {
  const key = `${process.platform} ${process.arch}`;
  const pkg = PLATFORMS[key];
  if (!pkg) {
    fail(
      `brain has no prebuilt binary for ${key}.`,
      "",
      "Supported: " + Object.keys(PLATFORMS).join(", ") + ".",
      "Build from source instead:",
      "  go install github.com/Coder8124/brain/cmd/brain@latest"
    );
  }

  const exe = process.platform === "win32" ? "brain.exe" : "brain";
  try {
    // Resolve through the package's own entry so npm/pnpm/yarn layouts, symlinks
    // and nested node_modules all work without guessing at directory structure.
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch (e) {
    fail(
      `brain is installed, but the binary package for ${key} is missing.`,
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
  for (const line of lines) console.error(line ? `brain: ${line}` : "");
  process.exit(1);
}

// spawnSync rather than spawn: this process has nothing to do while the binary
// runs, and inheriting stdio means the JSON-RPC stream flows through the real
// descriptors with no Node layer in between.
const result = spawnSync(binaryPath(), process.argv.slice(2), {
  stdio: "inherit",
  windowsHide: true,
});

if (result.error) {
  if (result.error.code === "EACCES") {
    fail(
      "the binary is not executable.",
      "",
      "Reinstall to restore its permissions:",
      "  npm install --force @brainyprime/brain"
    );
  }
  fail(`could not start the binary: ${result.error.message}`);
}

// A child killed by a signal has a null status. Reproduce the signal rather
// than inventing an exit code, so `brain mcp serve` under a host that kills its
// servers reports what actually happened.
if (result.signal) {
  process.kill(process.pid, result.signal);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
