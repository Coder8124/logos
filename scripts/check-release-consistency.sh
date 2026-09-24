#!/usr/bin/env bash
#
# Do the three places a user can get Logos agree on what the current version is?
#
# A beta tester found the Homebrew formula one version behind the site. That is
# the worst kind of version skew, because neither half looks broken: the site
# advertises v0.4.4 and links its release notes, `brew install` succeeds, and
# the user ends up on 0.4.3 with a bug the release notes say was fixed. They
# have no reason to suspect the package manager, so the bug gets reported again
# against a version that does not have it.
#
# The cause is that the three are published by three different actions — a tag
# pushes the GitHub release and npm, the site is committed here, and the tap is
# a separate repository pushed by hand. Nothing checked that they landed
# together. This does.
#
# Run it after a release, and before announcing one:
#
#   scripts/check-release-consistency.sh
#
# Exits non-zero naming whichever is behind. Reads the network (the tap and the
# npm registry); that is a release tool, not the product, and the product's
# "nothing leaves the machine" invariant is unaffected.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0
note() { printf '%-22s %s\n' "$1" "$2"; }

# The site's own claim: the release its hero links to. That link is what a
# reader treats as "the current version", so it is the reference the others are
# measured against rather than one opinion among four.
site=$(sed -n 's|.*/releases/tag/v\([0-9][0-9.]*\)".*|\1|p' docs/index.html | head -1)
[ -n "$site" ] || { echo "could not read a version from docs/index.html"; exit 2; }
note "site" "$site"

# The files a release has to bump by hand. 0.4.6 bumped two of them, so the
# binary it shipped carried a plugin manifest one version behind itself and
# `logos doctor` warned every user of a skew they had no way to clear. The
# skew is invisible from inside any one file, which is why it survived two
# releases; checking them together is the only place it shows.
check_local() {
  local label="$1" path="$2" key="$3" got
  got=$(sed -n "s/.*\"$key\": *\"\([0-9][0-9.]*\)\".*/\1/p" "$path" | head -1)
  note "$label" "${got:-unknown}"
  [ "$got" = "$site" ] || { note "" "^ does not match the site"; fail=1; }
}
check_local "npm/package.json"        npm/package.json                   version
check_local "plugin manifest"         plugin/.claude-plugin/plugin.json  version
check_local "marketplace"             .claude-plugin/marketplace.json    version

# Published, not local. A formula committed in the tap checkout and never pushed
# is exactly the state that produced the original report.
tap=$(curl -fsSL --max-time 20 \
  https://raw.githubusercontent.com/Coder8124/homebrew-tap/main/Formula/logos-mcp.rb 2>/dev/null \
  | sed -n 's|.*/releases/download/v\([0-9][0-9.]*\)/.*|\1|p' | head -1)
if [ -z "$tap" ]; then
  note "brew tap" "unreachable — could not check"
  fail=1
else
  note "brew tap" "$tap"
  [ "$tap" = "$site" ] || { note "" "^ brew installs $tap while the site advertises $site"; fail=1; }
fi

registry=$(curl -fsSL --max-time 20 https://registry.npmjs.org/@noeton/logos/latest 2>/dev/null \
  | sed -n 's/.*"version":"\([0-9][0-9.]*\)".*/\1/p' | head -1)
if [ -z "$registry" ]; then
  note "npm latest" "unreachable — could not check"
  fail=1
else
  note "npm latest" "$registry"
  [ "$registry" = "$site" ] || { note "" "^ npx installs $registry while the site advertises $site"; fail=1; }
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "every install route serves $site"
else
  echo "release routes disagree — a user following the site gets a different version than it advertises"
fi
exit "$fail"
