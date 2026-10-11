#!/usr/bin/env bash
#
# Check the commit messages a branch adds against the house rules in
# CONTRIBUTING.md. CI runs it on every pull request; run it yourself before
# pushing:
#
#   scripts/check-commits.sh origin/main
#
# Only what has held for the whole history is enforced. Capitalisation is not:
# a subject that starts with an identifier ("logos import …", "textmatch …")
# is lowercase on purpose.
set -euo pipefail

base="${1:?usage: check-commits.sh <base-ref> [head-ref]}"
head="${2:-HEAD}"

bad=0
fail() {
  echo "  $1: $2"
  bad=1
}

for sha in $(git rev-list --no-merges "${base}..${head}"); do
  short=$(git rev-parse --short "$sha")
  subject=$(git log -1 --format=%s "$sha")
  body=$(git log -1 --format=%b "$sha")

  # Left over from an interactive rebase that was never finished.
  case "$subject" in
    fixup!* | squash!* | amend!*) fail "$short" "unsquashed: $subject" ;;
  esac
  [[ "$subject" == *. ]] && fail "$short" "subject ends with a period: $subject"
  # Attribution of any kind — a trailer or a generated-by line — stays out of
  # the history, whoever or whatever wrote the change. A generated-with line
  # is one that names its tool or links it, past a leading emoji: "generated
  # with go generate" in an ordinary body failed the check, and anchoring to
  # the line start alone still failed it wherever the body wrapped before it.
  if grep -qiE '^((co-authored-by|signed-off-by|generated-by):|[^[:alnum:]]*generated with (\[|claude|copilot|chatgpt|codex|cursor|gemini|aider|devin|windsurf))' <<<"$body"; then
    fail "$short" "attribution line in the message: $subject"
  fi
done

if [ "$bad" -ne 0 ]; then
  echo
  echo "commit messages above break CONTRIBUTING.md's rules; reword them with git rebase"
  exit 1
fi
echo "commit messages ok"
