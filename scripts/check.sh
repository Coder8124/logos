#!/usr/bin/env bash
#
# "Before you say it works", as one command: the checks CI runs that a laptop
# can, in the order that fails fastest.
#
#   scripts/check.sh               # this branch against origin/main
#   scripts/check.sh <base-ref>
#
# A LOGOS_VAULT or LOGOS_EMBED left in the shell reaches every test that does
# not set its own, and a run that passes or fails because of it says nothing
# about the code; CI's environment has none, so neither does this.
set -euo pipefail

base="${1:-origin/main}"
cd "$(git rev-parse --show-toplevel)"

for v in $(compgen -e); do
  case "$v" in LOGOS_* | BRAIN_*) unset "$v" ;; esac
done

step() {
  echo "== $1"
  shift
  "$@"
}

ignored() {
  if git ls-files -ci --exclude-standard | grep .; then
    echo "tracked above, but .gitignore keeps them out"
    return 1
  fi
}

formatted() {
  if gofmt -l . | grep -v '\.venv' | grep .; then
    echo "unformatted files above"
    return 1
  fi
}

step "nothing ignored is tracked" ignored
step "format" formatted
step "build" go build ./...
step "vet" go vet ./...
step "commit messages" scripts/check-commits.sh "$base" HEAD
step "test names" scripts/check-test-names.sh "$base" HEAD
step "test" go test ./...
step "chaos" go test -count=1 -tags chaos ./chaos/...
step "bugfixes are proved" scripts/check-bug-tests.sh "$base" HEAD
echo
echo "every check passed"
