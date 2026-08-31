#!/usr/bin/env node
"use strict";

// Publishes the platform packages first, then the wrapper.
//
// The order is not a preference. The wrapper's optionalDependencies pin exact
// versions, so publishing it first leaves every installer resolving packages
// that do not exist yet — a broken install for as long as the gap lasts, on the
// package people are told to run with npx.
//
//   node scripts/build.js
//   node scripts/test.js
//   node scripts/publish.js --dry-run     # see exactly what would go out
//   node scripts/publish.js --otp 123456  # if the account has 2FA on publish
//
// Scoped packages are private by default, hence --access public on every one.
//
// On 2FA: npm requires a one-time code per publish, and there are six packages
// here. A single code is usually accepted for all of them because npm honours it
// for a short window, but the codes rotate every 30 seconds and ~27MB has to go
// up — so take the code at the START of its window, or better, use a granular
// access token with "bypass 2FA" enabled:
//
//   npmjs.com → Access Tokens → Granular → allow @brainyprime/* → bypass 2FA
//   echo "//registry.npmjs.org/:_authToken=npm_xxx" >> ~/.npmrc
//
// A resumed run is safe: npm refuses to republish a version that already
// exists, so re-running after a partial failure skips what landed and continues.

const fs = require("fs");
const path = require("path");
const { execFileSync, spawnSync } = require("child_process");

const root = path.join(__dirname, "..");
const wrapper = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
const VERSION = wrapper.version;
const dryRun = process.argv.includes("--dry-run");

function die(msg) {
  console.error(`publish: ${msg}`);
  process.exit(1);
}

// Publishing is irreversible — a version number can never be reused, even after
// unpublishing. So every precondition is checked before anything is sent.
let who;
try {
  who = execFileSync("npm", ["whoami"], { encoding: "utf8" }).trim();
} catch {
  die("not logged in — run `npm login` first");
}

const platforms = path.join(root, "platforms");
if (!fs.existsSync(platforms)) die("no platforms/ — run: node scripts/build.js");

const dirs = fs.readdirSync(platforms).filter((d) =>
  fs.existsSync(path.join(platforms, d, "package.json"))
);
if (dirs.length === 0) die("platforms/ is empty — run: node scripts/build.js");

// A platform package stamped with a different version than the wrapper means
// build.js ran against a stale dist/. Catch it here rather than after three
// packages are already public.
for (const d of dirs) {
  const pkg = JSON.parse(fs.readFileSync(path.join(platforms, d, "package.json"), "utf8"));
  if (pkg.version !== VERSION) {
    die(`platforms/${d} is at ${pkg.version} but the wrapper is at ${VERSION} — re-run build.js`);
  }
  const exe = d.startsWith("win32") ? "brain.exe" : "brain";
  if (!fs.existsSync(path.join(platforms, d, "bin", exe))) {
    die(`platforms/${d} has no binary — re-run build.js`);
  }
}

console.log(`publishing brain ${VERSION} as ${who}${dryRun ? " (dry run)" : ""}\n`);

const otpFlag = process.argv.indexOf("--otp");
const otp = otpFlag !== -1 ? process.argv[otpFlag + 1] : null;

let published = 0;
function publish(dir, label) {
  const args = ["publish", "--access", "public"];
  if (dryRun) args.push("--dry-run");
  if (otp) args.push("--otp", otp);

  const r = spawnSync("npm", args, { cwd: dir, encoding: "utf8" });
  const out = (r.stdout || "") + (r.stderr || "");

  if (r.status === 0) {
    published++;
    console.log(`  ✓ ${label}`);
    return;
  }
  // Already published at this version is not a failure on a resumed run — it is
  // the thing that makes resuming safe.
  if (/cannot publish over|previously published|EPUBLISHCONFLICT/i.test(out)) {
    console.log(`  = ${label} (already at this version)`);
    return;
  }
  if (/one-time pass|otp|two-factor/i.test(out)) {
    die(
      `${label} needs a 2FA code.\n\n` +
        `  Re-run with a fresh one:  node scripts/publish.js --otp <code>\n` +
        `  Or use a granular token with "bypass 2FA" (see the header of this file).\n\n` +
        `  ${published} package(s) published before this point; re-running skips them.`
    );
  }
  console.error(out.trim());
  die(`${label} failed — ${published} published, nothing after it`);
}

// Platform packages first.
for (const d of dirs) {
  publish(path.join(platforms, d), `@brainyprime/brain-${d}`);
}
// Then the wrapper that depends on them.
publish(root, "@brainyprime/brain");

console.log(
  dryRun
    ? "\nDry run only. Nothing was published."
    : `\nPublished. Try it:\n  npx -y @brainyprime/brain@${VERSION} version`
);
