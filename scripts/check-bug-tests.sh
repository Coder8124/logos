#!/usr/bin/env bash
#
# Does every bugfix land with a test that fails without it? CONTRIBUTING.md asks
# for that, and for the fix to be proved: a test that passes on the old code is
# not testing the fix, however well it is named. Nothing checked it, so the
# rule held only as long as every author remembered to run the test both ways.
#
# For each commit in the range whose subject starts "BUG: ", this takes the
# tests the commit adds or changes and runs them twice in a scratch clone:
# once at the commit, where they must pass, and once with every file the commit
# changed outside its tests put back as it was, where at least one must fail.
#
#   scripts/check-bug-tests.sh origin/main          # what this branch adds
#   scripts/check-bug-tests.sh <sha>^ <sha>         # one commit
#
# The old code comes from restoring the commit's non-test files, not from `git
# stash`: a stash takes the new test with it, the run finds no test to run, and
# a check that cannot fail proves nothing. The scratch copy is a clone rather
# than a `git worktree`: Logos files a linked worktree's checkpoints under the
# worktree's name, so a test that reads them back fails in one for a reason
# that has nothing to do with the commit. And it lives inside .git, not under
# mktemp: setup's tests refuse to run from a checkout in a temporary directory,
# since a vault there would not last.
set -euo pipefail

base="${1:?usage: check-bug-tests.sh <base-ref> [head-ref]}"
head="${2:-HEAD}"

# A vault or a model named in the caller's shell would reach the tests and make
# them pass or fail for a reason that has nothing to do with the commit.
for v in $(compgen -e); do
  case "$v" in LOGOS_* | BRAIN_*) unset "$v" ;; esac
done

bad=0
wt=""
cleanup() {
  if [ -n "$wt" ]; then
    rm -rf "$wt"
    wt=""
  fi
}
trap cleanup EXIT

# Run the named tests in one package and print one "<STATUS> <name>" line per
# top-level test, or "BUILD" if the package did not compile.
run() {
  local dir="$1" names="$2" tags=""
  if grep -lqs '^//go:build.*chaos' "$wt/$dir"/*_test.go; then
    tags="-tags chaos"
  fi
  local out
  # shellcheck disable=SC2086 # tags is one flag or none
  out=$(cd "$wt" && go test $tags -count=1 -v -run "^($(paste -sd'|' - <<<"$names"))\$" "./$dir" 2>&1) || true
  if grep -qE '\[(build|setup) failed\]' <<<"$out"; then
    echo BUILD
    return
  fi
  sed -nE 's/^--- (PASS|FAIL|SKIP): ([A-Za-z0-9_]+) .*/\1 \2/p' <<<"$out"
}

for sha in $(git rev-list --reverse --no-merges "${base}..${head}"); do
  subject=$(git log -1 --format=%s "$sha")
  [[ "$subject" == "BUG: "* ]] || continue
  short=$(git rev-parse --short "$sha")

  # Everything the commit changed except its tests and their fixtures. Empty,
  # the bug was in a test itself — a flake, a leak between tests — and there is
  # no code to put back: the fixed test passing is the proof.
  fixed=$(git diff --name-only --no-renames "${sha}^" "$sha" | { grep -vE '_test\.go$|(^|/)testdata/' || true; })
  if [ -z "$fixed" ]; then
    echo "  ok $short: fixes only tests, nothing to run without the fix"
    continue
  fi

  # A test the commit adds, or one whose body it changes: the hunk header names
  # the function a change sits in. A hunk just after a test can name that test
  # too, which only adds a name to the run. One "<package dir> <test>" per line.
  tests=$(git diff --name-only --diff-filter=AM "${sha}^" "$sha" -- '*_test.go' |
    while IFS= read -r f; do
      git diff -U0 "${sha}^" "$sha" -- "$f" |
        sed -nE 's/^\+func (Test[A-Za-z0-9_]+)\(.*/\1/p; s/^@@ .* @@ func (Test[A-Za-z0-9_]+)\(.*/\1/p' |
        { grep -vx TestMain || true; } | sed "s|^|$(dirname "$f") |"
    done | sort -u)

  if [ -z "$tests" ]; then
    echo "  $short: adds or changes no test: $subject"
    bad=1
    continue
  fi
  dirs=$(cut -d' ' -f1 <<<"$tests" | sort -u)

  wt=$(mktemp -d "$(git rev-parse --absolute-git-dir)/check-bug-tests.XXXXXX")
  git clone -q --shared --no-checkout "$(git rev-parse --show-toplevel)" "$wt"
  git -C "$wt" checkout -q --detach "$sha"

  with=""
  for d in $dirs; do
    with+=$(run "$d" "$(sed -n "s|^$d ||p" <<<"$tests")")$'\n'
  done

  # The old code: the files in $fixed as they were in the parent. A file the
  # commit added is removed.
  while IFS= read -r p; do
    if git cat-file -e "${sha}^:$p" 2>/dev/null; then
      git -C "$wt" checkout -q "${sha}^" -- "$p"
    else
      rm -f "$wt/$p"
    fi
  done <<<"$fixed"

  without=""
  for d in $dirs; do
    without+=$(run "$d" "$(sed -n "s|^$d ||p" <<<"$tests")")$'\n'
  done
  cleanup

  if grep -qx BUILD <<<"$with"; then
    echo "  $short: its tests do not compile: $subject"
    bad=1
  elif failing=$(sed -n 's/^FAIL //p' <<<"$with") && [ -n "$failing" ]; then
    echo "  $short: fails even with the fix ($(paste -sd' ' - <<<"$failing")): $subject"
    bad=1
  elif grep -qx BUILD <<<"$without"; then
    echo "  ok $short: its tests do not compile without the fix"
  elif failing=$(sed -n 's/^FAIL //p' <<<"$without") && [ -n "$failing" ]; then
    echo "  ok $short: $(paste -sd' ' - <<<"$failing") fails without the fix"
  elif [ -z "$(sed '/^$/d' <<<"$without")" ]; then
    echo "  $short: none of its changed tests ran: $subject"
    bad=1
  else
    echo "  $short: every test it changes passes without the fix: $subject"
    bad=1
  fi
done

if [ "$bad" -ne 0 ]; then
  echo
  echo "a BUG: commit above lands without a test that fails without it (CONTRIBUTING.md)"
  exit 1
fi
echo "bugfix tests ok"
