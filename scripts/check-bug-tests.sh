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

# Words a //go:build line can name that the toolchain sets on its own: passed
# as -tags they would build another platform's files here.
implied=" $(go tool dist list | tr '/' '\n' | sort -u | tr '\n' ' ') unix cgo gc gccgo "

# Run the named tests in one package and print one "<STATUS> <name>" line per
# top-level test; "BUILD" if the package did not compile, "DIED" if the test
# binary failed without failing a test. That last is a hang hitting the timeout
# or an os.Exit, and it prints no "--- FAIL" line, only the package's FAIL.
run() {
  local dir="$1" names="$2" n f w tags="" race=""
  # The tags the tests' own files are built under. Only chaos was passed once,
  # and a test behind any other tag never ran.
  for n in $names; do
    f=$(grep -l "^func $n(" "$wt/$dir"/*_test.go 2>/dev/null | head -1) || true
    [ -n "$f" ] || continue
    for w in $(sed -n 's|^//go:build ||p' "$f" | tr -c 'A-Za-z0-9_.!\n' ' '); do
      case "$w" in
        '!'* | go1.*) ;;
        race) race=-race ;;
        *) [[ "$implied" == *" $w "* ]] || tags+="${tags:+,}$w" ;;
      esac
    done
  done
  local out rc=0
  # shellcheck disable=SC2086 # race is one flag or none
  out=$(cd "$wt" && go test $race ${tags:+-tags "$tags"} -count=1 -timeout 5m -v \
    -run "^($(paste -sd'|' - <<<"$names"))\$" "./$dir" 2>&1) || rc=$?
  if grep -qE '\[(build|setup) failed\]' <<<"$out"; then
    echo BUILD
    return
  fi
  out=$(sed -nE 's/^--- (PASS|FAIL|SKIP): ([A-Za-z0-9_]+) .*/\1 \2/p' <<<"$out")
  if [ "$rc" -ne 0 ] && ! grep -q '^FAIL ' <<<"$out"; then
    echo DIED
    return
  fi
  printf '%s\n' "$out"
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
  # the function a change sits in. A hunk that adds a test names only that
  # test: its header names the function above it, and that neighbour passing
  # made a new test that skips here read as passing without the fix. One
  # "<package dir> <test>" per line.
  tests=$(git diff --name-only --diff-filter=AM "${sha}^" "$sha" -- '*_test.go' |
    while IFS= read -r f; do
      git diff -U0 "${sha}^" "$sha" -- "$f" | awk '
        function flush() { if (added == "" && header != "") print header; printf "%s", added; header = ""; added = "" }
        /^@@ / { flush(); if (match($0, /@@ func Test[A-Za-z0-9_]+\(/)) header = substr($0, RSTART + 8, RLENGTH - 9); next }
        /^\+func Test[A-Za-z0-9_]+\(/ { match($0, /Test[A-Za-z0-9_]+/); added = added substr($0, RSTART, RLENGTH) "\n" }
        END { flush() }' |
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

  ran=$(sed '/^$/d' <<<"$without")
  if grep -qx BUILD <<<"$with"; then
    echo "  $short: its tests do not compile: $subject"
    bad=1
  elif grep -qx DIED <<<"$with"; then
    echo "  $short: its test binary dies even with the fix (a hang, an os.Exit): $subject"
    bad=1
  elif failing=$(sed -n 's/^FAIL //p' <<<"$with") && [ -n "$failing" ]; then
    echo "  $short: fails even with the fix ($(paste -sd' ' - <<<"$failing")): $subject"
    bad=1
  elif grep -qx BUILD <<<"$without"; then
    echo "  ok $short: its tests do not compile without the fix"
  elif grep -qx DIED <<<"$without"; then
    echo "  ok $short: its test binary dies without the fix (a hang, an os.Exit)"
  elif failing=$(sed -n 's/^FAIL //p' <<<"$without") && [ -n "$failing" ]; then
    echo "  ok $short: $(paste -sd' ' - <<<"$failing") fails without the fix"
  elif [ -z "$ran" ]; then
    echo "  $short: none of its changed tests ran: $subject"
    bad=1
  elif ! grep -qv '^SKIP ' <<<"$ran"; then
    # Not a rejection: a fix that needs a model runtime or another OS has no
    # way to be proved on this runner. Not a pass either — it says so.
    echo "  unproved $short: its tests skip themselves here ($(sed -n 's/^SKIP //p' <<<"$ran" | paste -sd' ' -)), so run them both ways where they can: $subject"
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
