#!/usr/bin/env bash
#
# Cross-compile the brain CLI for the platforms people actually run it on, and
# write checksums beside the archives.
#
#   ./scripts/release.sh            # builds as "dev"
#   ./scripts/release.sh v0.1.0     # stamps the version into the binary
#
# Output lands in dist/. Everything is static: the SQLite driver is modernc's
# pure-Go one, so there is no cgo and no libc to match — a single file that runs
# on a machine with nothing else installed.
#
# The desktop app is not built here. Wails needs platform toolchains and code
# signing that do not cross-compile, so `cd app && wails build` stays a separate,
# per-platform step.
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-dev}"
OUT="dist"
rm -rf "$OUT"
mkdir -p "$OUT"

# CGO off is what makes these portable. Trimpath keeps local paths out of the
# binary; -s -w drops the symbol table and DWARF, roughly halving the size of
# something nobody is going to debug from a tarball.
export CGO_ENABLED=0
LDFLAGS="-s -w -X github.com/Coder8124/brain/internal/buildinfo.Version=${VERSION}"

platforms=(
  "darwin arm64"    # Apple silicon
  "darwin amd64"    # Intel Macs
  "linux amd64"
  "linux arm64"     # servers, Raspberry Pi, WSL2 on ARM
  "windows amd64"
)

echo "building brain ${VERSION}"
for p in "${platforms[@]}"; do
  read -r goos goarch <<<"$p"

  name="brain"
  [ "$goos" = "windows" ] && name="brain.exe"

  dir="${OUT}/brain_${VERSION}_${goos}_${goarch}"
  mkdir -p "$dir"

  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" \
    -o "${dir}/${name}" ./cmd/brain

  # The readme and licence travel with the binary; someone who downloads a
  # tarball should not have to go looking for either. Missing files are noted
  # rather than swallowed — a release that quietly ships without a licence is
  # a release nobody at a company is allowed to use.
  for doc in README.md LICENSE; do
    src="$doc"
    [ -f "$src" ] || src="yap files/$doc"
    if [ -f "$src" ]; then
      cp "$src" "${dir}/${doc}"
    else
      echo "  warning: no ${doc} to ship" >&2
    fi
  done

  if [ "$goos" = "windows" ]; then
    (cd "$OUT" && zip -qr "$(basename "$dir").zip" "$(basename "$dir")")
  else
    tar -czf "${dir}.tar.gz" -C "$OUT" "$(basename "$dir")"
  fi
  rm -rf "$dir"

  echo "  ${goos}/${goarch}"
done

# One checksum file for the whole release, which is what a package manager or a
# careful human will actually verify against.
(cd "$OUT" && shasum -a 256 ./*.tar.gz ./*.zip 2>/dev/null > SHA256SUMS || true)

echo
ls -lh "$OUT"
echo
echo "checksums:"
cat "${OUT}/SHA256SUMS"
