# Packaging Logos for npm

How this directory works, for whoever maintains it. The page users see is
[README.md](README.md), which is what npmjs.com renders — it ships inside the
wrapper package, and this file deliberately does not.

`npx` is the shortest path from nothing to Logos answering inside an agent, and
it is the convention MCP hosts already use, which is why this is the primary
distribution channel rather than an afterthought behind a `go install`.

## Two names, on purpose

**Logos** is the product. **brain** is the development name — the Go module
(`github.com/Coder8124/logos`), the repository, the command the binary calls itself
in its own help, and `BRAIN_VAULT`.

The seam is exactly one file, `bin/logos.js`: the npm packages carry the product
name, and the executable inside them keeps the development name. Nothing in the
Go tree was renamed to publish this, which is why the two can be settled
independently.

The wrapper installs `logos` and `brain` as the same command, so either reads
correctly next to whichever set of docs you are looking at.

## How it is packaged

One wrapper package plus five platform packages, the pattern esbuild and swc
use:

```
@brainyprime/logos                 the launcher, a few KB
  ├─ @brainyprime/logos-darwin-arm64      os: darwin  cpu: arm64
  ├─ @brainyprime/logos-darwin-x64        os: darwin  cpu: x64
  ├─ @brainyprime/logos-linux-x64         os: linux   cpu: x64
  ├─ @brainyprime/logos-linux-arm64       os: linux   cpu: arm64
  └─ @brainyprime/logos-win32-x64         os: win32   cpu: x64
```

The platform packages are `optionalDependencies` carrying `os` and `cpu` fields,
so npm evaluates them at install time and fetches only the one that matches —
about 11 MB, not 55.

**No `postinstall` script.** The common alternative downloads a binary after
install, which breaks under `--ignore-scripts` (increasingly the default in CI),
breaks offline installs, and asks users to trust a network fetch at install time.
Shipping the binaries as real package contents costs nothing and avoids all
three.

The binaries are pure Go with `CGO_ENABLED=0`, so one linux build covers glibc
and musl alike.

### The launcher

`bin/logos.js` resolves the platform package and hands over with `stdio:
"inherit"`, so the child gets the real file descriptors. This matters more than
it looks: `logos mcp serve` speaks newline-delimited JSON-RPC over stdin/stdout,
and a wrapper that buffers or writes one stray byte to stdout corrupts the
stream. Node is not in the data path, and every diagnostic goes to stderr.

Exit codes and signals are propagated, so a host that kills its servers sees
what actually happened.

## Releasing

Push a tag and let `.github/workflows/release.yml` do it — it builds, tests,
publishes all six packages over npm's trusted publishing, and cuts the GitHub
release. There is no token anywhere in that path.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

Trusted publishing has to be configured per package on npmjs.com, and a package
must exist before it can be configured, so the **first** publish of each of the
six is manual. Once, with 2FA:

```bash
../scripts/release.sh v0.1.0     # cross-compile, checksum
node scripts/build.js            # generate platforms/ from dist/
node scripts/test.js             # handshake through the wrapper
node scripts/publish.js --otp 123456
```

`publish.js` does the platform packages before the wrapper — the wrapper's
`optionalDependencies` pin exact versions, and publishing it first leaves
everyone with a broken install until the rest land. It also refuses to start
without valid auth, checks every platform package's version against the
wrapper's, and is safe to re-run after a partial failure: npm rejects a
republish, and the script reads that as "already landed" rather than an error.

Public packages are free and unlimited on npm. Scoped packages default to
private, which is why `--access public` is on every publish.

`build.js` extracts the binaries from the release archives rather than compiling
its own, so what npm ships is byte-identical to the GitHub release and covered
by the same `SHA256SUMS`.

Bump the version in `package.json` — `build.js` reads it, stamps every platform
package with it, and looks for `dist/brain_v<version>_*` archives, so a mismatch
between the tag and the package version is caught as a missing archive rather
than shipping quietly.

### What `test.js` checks

Beyond "does it run": it drives a real `initialize` handshake through the
launcher and asserts stdout carries only JSON-RPC. That check is the reason this
directory has a test at all — it is the failure mode a wrapper introduces, and
it is invisible until a host reports an unhelpful protocol error.

It also caught a bug in `mcp serve` itself: the server opened the SQLite file
directly instead of going through `index.Open`, so it failed on a vault that had
never been indexed, ran without `busy_timeout`, and never registered the vault
for memory writes.

## Scope

`@brainyprime` must match the npm account or org publishing it. If the scope
differs, change it in `package.json`, in the `PLATFORMS` table in `bin/logos.js`,
and where `build.js` names each platform package — all three must agree or the
launcher will not resolve its own binary.

The unscoped `logos` and `logos-cli` are both taken by unrelated packages, and
the `@logos` scope is already registered, so the scoped name is the one that is
actually ours to publish.

## Layout

```
npm/
  package.json          the wrapper
  README.md             the npmjs.com page — ships inside the package
  PACKAGING.md          this file — does not ship
  bin/logos.js          resolve the platform package, exec it, pass stdio through
  scripts/build.js      generate platforms/ from ../dist
  scripts/test.js       run the launcher, drive an MCP handshake
  scripts/publish.js    platform packages, then the wrapper
  platforms/            generated, gitignored
```

`files` in `package.json` is an allowlist, so adding a document here does not
publish it. Anything users should read on npmjs.com has to go in README.md and
be listed there.
