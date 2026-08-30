# brain on npm

`npx` is the shortest path from nothing to brain answering inside an agent, and
it is the convention MCP hosts already use. No Go toolchain, no clone, no build.

```bash
npx -y @coder8124/brain setup
```

Or wire a host by hand, with no install at all:

```json
{
  "mcpServers": {
    "brain": {
      "command": "npx",
      "args": ["-y", "@coder8124/brain", "mcp", "serve"],
      "env": { "BRAIN_VAULT": "/Users/you/brain" }
    }
  }
}
```

That config is portable between machines, which the binary-path form is not.

## How it is packaged

One wrapper package plus five platform packages, the pattern esbuild and swc
use:

```
@coder8124/brain                 the launcher, a few KB
  ├─ @coder8124/brain-darwin-arm64      os: darwin  cpu: arm64
  ├─ @coder8124/brain-darwin-x64        os: darwin  cpu: x64
  ├─ @coder8124/brain-linux-x64         os: linux   cpu: x64
  ├─ @coder8124/brain-linux-arm64       os: linux   cpu: arm64
  └─ @coder8124/brain-win32-x64         os: win32   cpu: x64
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

`bin/brain.js` resolves the platform package and hands over with `stdio:
"inherit"`, so the child gets the real file descriptors. This matters more than
it looks: `brain mcp serve` speaks newline-delimited JSON-RPC over stdin/stdout,
and a wrapper that buffers or writes one stray byte to stdout corrupts the
stream. Node is not in the data path, and every diagnostic goes to stderr.

Exit codes and signals are propagated, so a host that kills its servers sees
what actually happened.

## Releasing

Platform packages must be published **before** the wrapper — its
`optionalDependencies` pin exact versions, and publishing the wrapper first
leaves everyone with a broken install until the rest land.

```bash
../scripts/release.sh v0.1.0     # cross-compile, checksum
node scripts/build.js            # generate platforms/ from dist/
node scripts/test.js             # handshake through the wrapper

for d in platforms/*/; do (cd "$d" && npm publish --access public); done
npm publish --access public
```

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

It also caught a bug in `brain mcp serve` itself: the server opened the SQLite
file directly instead of going through `index.Open`, so it failed on a vault
that had never been indexed, ran without `busy_timeout`, and never registered
the vault for memory writes.

## Scope

`@coder8124` must match the npm account or org publishing it. If the scope
differs, change it in `package.json`, in the `PLATFORMS` table in
`bin/brain.js`, and in `TARGETS` in `scripts/build.js` — all three must agree or
the launcher will not resolve its own binary.

## Layout

```
npm/
  package.json          the wrapper
  bin/brain.js          resolve the platform package, exec it, pass stdio through
  scripts/build.js      generate platforms/ from ../dist
  scripts/test.js       run the launcher, drive an MCP handshake
  platforms/            generated, gitignored
```
