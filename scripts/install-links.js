#!/usr/bin/env node
"use strict";

// Generates the one-click install links, so the README's buttons can never
// drift from the config they claim to install.
//
//   node scripts/install-links.js            # print the markdown
//   node scripts/install-links.js --check    # verify README matches, exit 1 if not
//
// Both Cursor and VS Code accept a deeplink carrying a base64 of the server
// config. That is the whole mechanism: a badge in the README becomes a real
// one-click install with no terminal, no JSON, and no absolute paths — which is
// only possible because the npm package resolves the binary itself.

const fs = require("fs");
const path = require("path");

const PKG = "@ankrainc/logos";

// The server name the host will show, and the prefix every tool appears under.
// It is the product name, not the development one: this is a surface the user
// reads. BRAIN_VAULT and the binary keep the development name; see npm/bin.
const NAME = "logos";

// No BRAIN_VAULT here on purpose. The binary defaults to ~/brain (an absolute
// path), so omitting it keeps the link portable between machines — and a link
// carrying one person's home directory is worse than no link at all.
const config = {
  command: "npx",
  args: ["-y", PKG, "mcp", "serve"],
};

const b64 = Buffer.from(JSON.stringify(config)).toString("base64");

// Cursor's documented format. Deeplinks cap at 8,000 characters; this is two
// orders of magnitude under, but check rather than assume.
const cursor = `cursor://anysphere.cursor-deeplink/mcp/install?name=${NAME}&config=${b64}`;

// VS Code takes the config as URL-encoded JSON rather than base64.
const vscodeCfg = encodeURIComponent(JSON.stringify({ name: NAME, ...config }));
const vscode = `vscode:mcp/install?${vscodeCfg}`;

for (const [name, url] of [["cursor", cursor], ["vscode", vscode]]) {
  if (url.length > 8000) {
    console.error(`install-links: ${name} link is ${url.length} chars, over the 8000 limit`);
    process.exit(1);
  }
}

const markdown = `[![Add to Cursor](https://img.shields.io/badge/Add%20to-Cursor-000000?style=flat-square&logo=cursor)](${cursor})
[![Add to VS Code](https://img.shields.io/badge/Add%20to-VS%20Code-007ACC?style=flat-square&logo=visualstudiocode)](${vscode})`;

if (process.argv.includes("--check")) {
  const readme = fs.readFileSync(path.join(__dirname, "..", "README.md"), "utf8");
  const missing = [cursor, vscode].filter((u) => !readme.includes(u));
  if (missing.length) {
    console.error(
      "install-links: README links are stale — regenerate with:\n" +
        "  node scripts/install-links.js\n\n" +
        "Stale because the encoded config no longer matches what this script produces."
    );
    process.exit(1);
  }
  console.log("install-links: README matches");
  process.exit(0);
}

console.log(markdown);
console.log("\n--- decoded, for review ---");
console.log(JSON.stringify(config, null, 2));
