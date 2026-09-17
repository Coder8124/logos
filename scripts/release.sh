#!/usr/bin/env bash
#
# Cross-compile the logos CLI for the platforms people actually run it on, and
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
# RELEASE_OUT and RELEASE_PLATFORMS exist for the test that runs this script
# (internal/selfupdate), so it can build one platform without clearing dist/.
OUT="${RELEASE_OUT:-dist}"
rm -rf "$OUT"
mkdir -p "$OUT"

# CGO off is what makes these portable. Trimpath keeps local paths out of the
# binary; -s -w drops the symbol table and DWARF, roughly halving the size of
# something nobody is going to debug from a tarball.
export CGO_ENABLED=0
LDFLAGS="-s -w -X github.com/Coder8124/logos/internal/buildinfo.Version=${VERSION}"

platforms=(
  "darwin arm64"    # Apple silicon
  "darwin amd64"    # Intel Macs
  "linux amd64"
  "linux arm64"     # servers, Raspberry Pi, WSL2 on ARM
  "windows amd64"
)
if [ -n "${RELEASE_PLATFORMS:-}" ]; then
  IFS=, read -r -a platforms <<<"$RELEASE_PLATFORMS"
fi

# Put a staged directory into its archive and remove the directory.
archive() {
  local dir="$1" goos="$2"
  if [ "$goos" = "windows" ]; then
    (cd "$OUT" && zip -qr "$(basename "$dir").zip" "$(basename "$dir")")
  else
    tar -czf "${dir}.tar.gz" -C "$OUT" "$(basename "$dir")"
  fi
  rm -rf "$dir"
}

echo "building logos ${VERSION}"
for p in "${platforms[@]}"; do
  read -r goos goarch <<<"$p"

  name="logos"
  [ "$goos" = "windows" ] && name="logos.exe"

  dir="${OUT}/logos_${VERSION}_${goos}_${goarch}"
  mkdir -p "$dir"

  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" \
    -o "${dir}/${name}" ./cmd/logos

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

  # The same binary again under the 0.4 name. A 0.4 `brain update` asks the
  # release for brain_<version>_<os>_<arch> holding a file called brain, and
  # without it every 0.4 install is stranded on 0.4 with "no asset" — the
  # rename would be the last update it ever saw. Through 0.4.x only; remove
  # before 0.5.0.
  old="brain"
  [ "$goos" = "windows" ] && old="brain.exe"
  olddir="${OUT}/brain_${VERSION}_${goos}_${goarch}"
  cp -R "$dir" "$olddir"
  mv "${olddir}/${name}" "${olddir}/${old}"

  archive "$dir" "$goos"
  archive "$olddir" "$goos"

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

# The archives are only one of three things a user can install from. Say so
# here, because the version skew this catches is invisible from inside a
# successful build.
echo
echo "after tagging and pushing the tap, check every route agrees:"
echo "  scripts/check-release-consistency.sh"
