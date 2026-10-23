#!/usr/bin/env bash
#
# Is every test a branch adds named as a sentence? CONTRIBUTING.md asks for
# `TestAForgottenIDIsNeverHandedOutAgain`, not `TestForget`: the name is what a
# failure prints, and a label tells whoever reads it nothing about what broke.
#
#   scripts/check-test-names.sh origin/main
#
# A sentence is taken to be at least four words after "Test", which is the
# shortest name any test on main has had since v0.4.3 (`TestNoHitsRendersNothing`). The
# labels from before then are left alone: only names new to the range are read,
# and a name that already exists at the base is not new, so moving a file of
# old tests does not make its author rename them.
set -euo pipefail

base="${1:?usage: check-test-names.sh <base-ref> [head-ref]}"
head="${2:-HEAD}"

# Words in a CamelCase name, splitting an acronym from the word after it:
# MCPServerStarts is three.
words() {
  sed -E 's/([a-z0-9])([A-Z])/\1 \2/g; s/([A-Z])([A-Z][a-z])/\1 \2/g; s/_/ /g' <<<"$1" | wc -w
}

from=$(git merge-base "$base" "$head")

bad=0
for name in $(git diff "${base}...${head}" -- '*_test.go' |
  sed -nE 's/^\+func (Test[A-Za-z0-9_]+)\(.*/\1/p' | sort -u); do
  [ "$name" = TestMain ] && continue
  if git grep -q "^func $name(" "$from" -- '*_test.go'; then
    continue
  fi
  if [ "$(words "${name#Test}")" -lt 4 ]; then
    echo "  $name: name the property it holds, as a sentence"
    bad=1
  fi
done

if [ "$bad" -ne 0 ]; then
  echo
  echo "test names above are labels, not sentences (CONTRIBUTING.md)"
  exit 1
fi
echo "test names ok"
