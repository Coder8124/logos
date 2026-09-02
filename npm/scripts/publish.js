#!/usr/bin/env node
"use strict";

// Publishes the platform packages first, then the wrapper.
//
// The order is not a preference. The wrapper's optionalDependencies pin exact
// versions, so publishing it first leaves every installer resolving packages
// that do not exist yet — a broken install for as long as the gap lasts, on the
// package people are told to run with npx.
//
//   npm run release:dry     # build, test, and show exactly what would go out
//   npm run release         # the same, then publish
//
// Or the steps on their own: build.js, test.js, publish.js.
//
// Scoped packages are private by default, hence --access public on every one.
// Public packages are free and unlimited; the flag is what keeps them out of
// the paid private tier, not a thing that costs anything.
//
// # How to authenticate, in the order you should prefer
//
// 1. Don't. Push a tag and let .github/workflows/release.yml publish through
//    npm's trusted publishing, where GitHub proves the repo and workflow to npm
//    over OIDC and no token exists to be stolen or to expire. This is the only
//    option that stays true when somebody else on the team cuts the release.
//
// 2. Enable 2FA on the account and pass a fresh code: `--otp 123456`. npm
//    honours one code for a short window, so all six packages usually go up on
//    one — take it at the START of its 30 second window, because ~27MB has to
//    upload before the last publish is attempted.
//
// 3. A granular token with "bypass 2FA". This used to be the easy answer and is
//    on its way out: npm now warns that bypass-2FA tokens are being restricted
//    for direct publishing (gh.io/npm-gat-bypass2fa-deprecation). Do not build a
//    release process on it.
//
// The first publish of a package is the one step that cannot use (1), because a
// trusted publisher is configured on a package that already exists. So: publish
// once by hand with (2), then configure trusted publishing for all six, then
// never touch a credential again.
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
// --ci says the identity comes from the CI provider's OIDC token, not from a
// login on this machine, so the interactive checks below do not apply.
const ci = process.argv.includes("--ci");

function die(msg) {
  console.error(`publish: ${msg}`);
  process.exit(1);
}

// Publishing is irreversible — a version number can never be reused, even after
// unpublishing. So every precondition is checked before anything is sent.
//
// Auth is checked first and hardest, because it is the one failure that costs
// real time: without this, a bad credential is discovered after the first 11MB
// platform package has already been uploaded and rejected.
let who = "the CI workflow";
if (!ci) {
  try {
    who = execFileSync("npm", ["whoami"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  } catch (e) {
    const out = (e.stdout || "") + (e.stderr || "");
    if (/E401|Unauthorized|token seems to be invalid/i.test(out)) {
      die(
        "the saved npm token is no longer valid.\n\n" +
          "  Log in again:  npm login\n\n" +
          "  Tokens expire and get revoked; this is why the release workflow\n" +
          "  publishes over OIDC instead. See the header of this file."
      );
    }
    die("not logged in — run `npm login` first");
  }
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

console.log(`publishing logos ${VERSION} as ${who}${dryRun ? " (dry run)" : ""}\n`);

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
  if (/one-time pass|otp|two-factor|bypass 2fa/i.test(out)) {
    die(
      `${label} was refused: npm wants proof of two-factor auth.\n\n` +
        `  Fastest way through, right now:\n` +
        `    1. Turn on 2FA at npmjs.com/settings/${who}/tfa (authenticator app, free)\n` +
        `    2. npm run release -- --otp <the 6 digits>\n` +
        `       Take the code at the start of its window — ~27MB uploads first.\n\n` +
        `  Then never again: configure trusted publishing on all six packages and\n` +
        `  cut releases by pushing a tag. See .github/workflows/release.yml.\n\n` +
        `  ${published} package(s) published before this point; re-running skips them.`
    );
  }
  console.error(out.trim());
  die(`${label} failed — ${published} published, nothing after it`);
}

// Platform packages first.
for (const d of dirs) {
  publish(path.join(platforms, d), `@brainyprime/logos-${d}`);
}
// Then the wrapper that depends on them.
publish(root, "@brainyprime/logos");

console.log(
  dryRun
    ? "\nDry run only. Nothing was published."
    : `\nPublished. Try it:\n  npx -y @brainyprime/logos@${VERSION} version`
);
