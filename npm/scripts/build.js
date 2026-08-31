#!/usr/bin/env node
"use strict";

// Generates the five platform packages from the archives scripts/release.sh
// produced, so what npm ships is byte-identical to what the GitHub release
// ships. One set of binaries, one set of checksums, no second build.
//
//   ../scripts/release.sh v0.1.0
//   node scripts/build.js
//   node scripts/test.js
//   npm publish --access public          # in each platforms/* then here
//
// Platform packages must be published BEFORE the wrapper: the wrapper's
// optionalDependencies point at exact versions, and npm resolves them at install
// time. Publishing the wrapper first gives everyone a broken install until the
// rest land.

const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");

const root = path.join(__dirname, "..");
const repo = path.join(root, "..");
const dist = path.join(repo, "dist");
const out = path.join(root, "platforms");

const wrapper = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
const VERSION = wrapper.version;

// Go's names on the left, npm's on the right. They disagree about x86-64 and
// about what to call Windows, which is the entire reason this table exists.
const TARGETS = [
  { npm: "darwin-arm64", goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { npm: "darwin-x64", goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { npm: "linux-x64", goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
  { npm: "linux-arm64", goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { npm: "win32-x64", goos: "windows", goarch: "amd64", os: "win32", cpu: "x64" },
];

function die(msg) {
  console.error(`build: ${msg}`);
  process.exit(1);
}

function archiveFor(t) {
  const base = `brain_v${VERSION}_${t.goos}_${t.goarch}`;
  const zip = path.join(dist, `${base}.zip`);
  const tgz = path.join(dist, `${base}.tar.gz`);
  if (fs.existsSync(tgz)) return { path: tgz, dir: base, zipped: false };
  if (fs.existsSync(zip)) return { path: zip, dir: base, zipped: true };
  return null;
}

// The archives hold brain_<version>_<os>_<arch>/brain. Extract to a scratch dir
// and lift the binary out rather than assuming a flat layout.
function extractBinary(archive, exe, destDir) {
  const scratch = fs.mkdtempSync(path.join(require("os").tmpdir(), "brain-npm-"));
  try {
    if (archive.zipped) {
      execFileSync("unzip", ["-q", archive.path, "-d", scratch], { stdio: "inherit" });
    } else {
      execFileSync("tar", ["-xzf", archive.path, "-C", scratch], { stdio: "inherit" });
    }
    const src = path.join(scratch, archive.dir, exe);
    if (!fs.existsSync(src)) die(`no ${exe} inside ${path.basename(archive.path)}`);
    fs.mkdirSync(destDir, { recursive: true });
    const dest = path.join(destDir, exe);
    fs.copyFileSync(src, dest);
    // npm preserves the executable bit through pack/publish; without it the
    // launcher fails with EACCES on first run.
    fs.chmodSync(dest, 0o755);
    return fs.statSync(dest).size;
  } finally {
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}

if (!fs.existsSync(dist)) {
  die(`no dist/ — run ../scripts/release.sh v${VERSION} first`);
}

fs.rmSync(out, { recursive: true, force: true });

let built = 0;
for (const t of TARGETS) {
  const archive = archiveFor(t);
  if (!archive) {
    console.error(`  ${t.npm.padEnd(14)} skipped — no archive for v${VERSION}`);
    continue;
  }
  const exe = t.goos === "windows" ? "brain.exe" : "brain";
  const dir = path.join(out, t.npm);
  const size = extractBinary(archive, exe, path.join(dir, "bin"));

  fs.writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify(
      {
        name: `@brainyprime/brain-${t.npm}`,
        version: VERSION,
        description: `brain binary for ${t.os} ${t.cpu}`,
        homepage: wrapper.homepage,
        repository: wrapper.repository,
        license: wrapper.license,
        // These two fields are the whole mechanism: npm evaluates them at
        // install time and skips every package that does not match the host,
        // so a user downloads one binary rather than five.
        os: [t.os],
        cpu: [t.cpu],
        files: [`bin/${exe}`],
        preferUnplugged: true,
      },
      null,
      2
    ) + "\n"
  );

  // Licence travels with the binary. A package that ships an executable and no
  // licence is one nobody at a company is allowed to use.
  const license = path.join(repo, "LICENSE");
  if (fs.existsSync(license)) {
    fs.copyFileSync(license, path.join(dir, "LICENSE"));
  } else {
    console.error(`  warning: no LICENSE at repo root to ship with ${t.npm}`);
  }

  console.log(`  ${t.npm.padEnd(14)} ${(size / 1048576).toFixed(1)} MB`);
  built++;
}

if (built === 0) die("nothing built");
console.log(`\n${built} platform package(s) in npm/platforms/ at v${VERSION}`);
