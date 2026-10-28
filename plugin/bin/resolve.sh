#!/usr/bin/env bash
#
# Find the Logos binary, and set LOGOS to the argv prefix that runs it.
#
# Sourced by every hook and by bin/mcp.sh, because they must agree: if the MCP
# server and the SessionStart hook resolve differently, a session resumes from
# one vault and checkpoints into another, and the handoff quietly stops working
# with nothing on screen to say so.
#
# PATH alone is not enough, and this is the failure that actually shipped. A
# `go install` build lands in ~/go/bin, which a login shell adds but a GUI-
# launched app does not inherit; on that machine `command -v logos` finds
# nothing while `logos` sits installed and working two directories away. The
# user's conclusion is that the plugin is broken, and they are right, but the
# cause is a search that stopped one directory short. So look in the places
# Go, Homebrew and npm actually install to, before giving up.
#
# Sets LOGOS as an array and returns 0, or returns 1 having set nothing. Either
# way LOGOS_REJECTED names the first candidate that was found but passed over.
#
# A candidate is run, not just found. A file that exists proves nothing: other
# npm packages install a `logos` bin of their own, and an npm install's shim
# dies with `env: node: No such file or directory` in an app launched from the
# Dock. Every build answers --version with "logos …". A pre-rename `brain`
# binary is not a candidate since 0.5.0: it reads only the BRAIN_ names, which
# these hooks no longer set, so it would run with every guard off.
#
# The newest working candidate wins, not the first one found. This used to take
# the first, and a `go install` build from months ago sitting earlier on PATH
# than a fresh `brew upgrade` won every time, silently. That is not one stale
# feature: every guard these hooks rely on is an environment variable an older
# binary ignores — LOGOS_NOTE_IF_UNCOMMITTED, LOGOS_AGENT, the auto checkpoint —
# so the plugin reverted to whatever the old binary did while its own files said
# otherwise, and the user had no way to see which copy answered.
#
# Ordering rules, in order: an unversioned build ("logos dev", what `go install`
# produces) loses to any numbered release, because the case that shipped is an
# old local build beating a current install and a dev build cannot say how old
# it is; among numbered releases the higher version wins; on a tie the earlier
# candidate wins, which keeps PATH ahead of the install directories. Set LOGOS_BIN to skip all of it — a
# developer running their own build needs a way to say so out loud.

# Which host this is. #95: setup pins the vault into each host's MCP config as
# LOGOS_VAULT, but hooks run with a bare environment and fell through to the
# machine pointer instead — so a session restored from one vault and
# checkpointed into another, each side consistent with itself and the user's
# work apparently vanishing. Naming the host lets the binary read that host's
# own pin. Not the vault itself: resolving it in bash would be the second
# implementation of a rule that already has one.
export LOGOS_HOST="${LOGOS_HOST:-claude-code}"

# logos_runs answers whether a candidate is a real Logos build, and sets
# LOGOS_VERSION to the version it reported. The version comes back from the same
# --version call that proves the binary works, so ranking candidates costs
# nothing beyond what checking them already cost.
logos_runs() {
  local out
  out=$("$@" --version 2>/dev/null)
  case "$out" in
    "logos "*)
      LOGOS_VERSION=${out#* }
      LOGOS_VERSION=${LOGOS_VERSION%% *}
      return 0
      ;;
  esac
  [ -n "$LOGOS_REJECTED" ] || LOGOS_REJECTED="$*"
  return 1
}

# logos_newer answers whether $1 is a newer version than $2. Dotted numbers
# compared field by field; anything not starting with a digit ("dev") is older
# than anything that does, and two unversioned builds never displace each other.
#
# One leading v is dropped first. A release archive is stamped from its tag and
# a `go install …@v0.4.9` build reports its module version, both with the v, so
# without this every real release ranked as "dev": two of them never displaced
# each other, and v0.4.9 lost to a bare 0.4.8 (#173).
logos_newer() {
  local a="${1#v}" b="${2#v}" x y
  case "$a" in [0-9]*) ;; *) return 1 ;; esac
  case "$b" in [0-9]*) ;; *) return 0 ;; esac
  while [ -n "$a" ] || [ -n "$b" ]; do
    x=${a%%.*}; y=${b%%.*}
    [ -n "$x" ] || x=0
    [ -n "$y" ] || y=0
    # Strip a pre-release or build suffix; 0.4.7-rc1 ranks as 0.4.7 rather than
    # failing the arithmetic and taking the whole comparison with it.
    x=${x%%[!0-9]*}; y=${y%%[!0-9]*}
    [ -n "$x" ] || x=0
    [ -n "$y" ] || y=0
    [ "$x" -gt "$y" ] 2>/dev/null && return 0
    [ "$x" -lt "$y" ] 2>/dev/null && return 1
    case "$a" in *.*) a=${a#*.} ;; *) a="" ;; esac
    case "$b" in *.*) b=${b#*.} ;; *) b="" ;; esac
  done
  return 1
}

logos_resolve() {
  local name dir cand best_ver="" best_desc="" first_desc="" dev_desc=""
  LOGOS_REJECTED=""
  # What won and what it reported, for logos_skew_notice. Kept apart from
  # LOGOS_VERSION, which is whatever candidate was probed last.
  LOGOS_RESOLVED="" LOGOS_RESOLVED_VERSION="" LOGOS_PASSED_DEV=""

  # An explicit choice is not a candidate to be ranked against others.
  if [ -n "${LOGOS_BIN:-}" ] && logos_runs "$LOGOS_BIN"; then
    LOGOS=("$LOGOS_BIN")
    LOGOS_RESOLVED="$LOGOS_BIN" LOGOS_RESOLVED_VERSION="$LOGOS_VERSION"
    return 0
  fi

  logos_consider() {
    local runner="$1" desc="$2"
    logos_runs "$runner" || return 1
    [ -n "$first_desc" ] || first_desc="$desc ($LOGOS_VERSION)"
    case "${LOGOS_VERSION#v}" in
      [0-9]*) ;;
      *) [ -n "$dev_desc" ] || dev_desc="$desc" ;;
    esac
    if [ -z "$best_ver" ] || logos_newer "$LOGOS_VERSION" "$best_ver"; then
      best_ver="$LOGOS_VERSION"
      best_desc="$desc ($LOGOS_VERSION)"
      LOGOS=("$runner")
      LOGOS_RESOLVED="$desc"
    fi
    return 0
  }

  # Every logos on PATH, not just the one `command -v` stops at. The whole
  # point of ranking is that a stale copy earlier on PATH must not hide a
  # newer one later on it, and `command -v` returns exactly the copy that
  # hides the rest.
  local oldifs="$IFS" path_dirs
  IFS=':' read -r -a path_dirs <<< "$PATH"
  IFS="$oldifs"
  for dir in "${path_dirs[@]}"; do
    [ -n "$dir" ] || dir="."
    cand="$dir/logos"
    [ -x "$cand" ] && logos_consider "$cand" "$cand"
  done
  # The fixed install locations can be replaced, and exist as a variable only
  # for the tests: they probed the real /opt/homebrew/bin whatever PATH said,
  # and passed for months only because the brew copy's "v0.4.8" was ranked as
  # an unversioned build and never won (#173). Unset means the real list.
  local fixed_dirs
  read -r -a fixed_dirs <<< "${LOGOS_RESOLVE_FIXED_DIRS-/opt/homebrew/bin /usr/local/bin}"
  for dir in "${GOBIN:-}" "${GOPATH:+$GOPATH/bin}" "$HOME/go/bin" \
             ${fixed_dirs[@]+"${fixed_dirs[@]}"} "$HOME/.local/bin"; do
    [ -n "$dir" ] || continue
    cand="$dir/logos"
    [ -x "$cand" ] && logos_consider "$cand" "$cand"
  done
  if [ -n "$best_desc" ]; then
    LOGOS_RESOLVED_VERSION="$best_ver"
    # #342: the ranking passes over a dev build for any numbered release, and a
    # developer whose `go install` sits beside an old brew copy got the old one
    # with nothing they would see — its fixed bugs then reported as current.
    # Kept whatever the PATH order, unlike the stderr line below: the dev build
    # being second on PATH does not make passing it over any more expected.
    [ "$LOGOS_RESOLVED" != "$dev_desc" ] && LOGOS_PASSED_DEV="$dev_desc"
    # Invariant 3: a choice the user cannot see is the whole of this bug. Said
    # only when the winner is not the copy they would get by typing `logos`,
    # because that is the case where the plugin and the shell disagree.
    if [ "$best_desc" != "$first_desc" ]; then
      printf 'logos: using %s rather than %s — newer\n' "$best_desc" "$first_desc" >&2
    fi
    return 0
  fi
  # Last resort: the published wrapper. Probed rather than assumed, so an
  # unpublished or offline registry fails here instead of at the first real
  # call, where it would look like Logos itself was broken.
  #
  # --prefer-offline because npx asks the registry before running even a cached
  # package: behind a proxy or offline that was 70 s per call, and Claude Code
  # gave up on the server long before. A cold cache still fetches.
  if command -v npx >/dev/null 2>&1 && npx --prefer-offline -y @noeton/logos --version >/dev/null 2>&1; then
    LOGOS=(npx --prefer-offline -y @noeton/logos)
    return 0
  fi
  return 1
}

# logos_plugin_version echoes the version this plugin was released as, read
# from its own manifest. That manifest is bumped with every release and held
# level with the binary's version by TestEveryFileThatStatesAVersionStatesTheSameOne,
# so it is the minimum logos these hooks were written against without being a
# second number someone has to remember to raise.
logos_plugin_version() {
  local manifest
  manifest="$(dirname "${BASH_SOURCE[0]}")/../.claude-plugin/plugin.json"
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest" 2>/dev/null | head -1
}

# logos_skew_notice echoes what the user needs to hear about the binary
# logos_resolve chose, one sentence per problem, or nothing. Called once, from
# the SessionStart hook, whose output is the only hook output the user is ever
# told about; record.sh runs on every tool call and must not pay for it.
#
# The direction that matters is a logos older than its plugin. Every guard the
# hooks rely on is an argument or a variable, and "a logos too old for the flag"
# ignores it — so the plugin's newer features switched off one by one with the
# hooks' own comments the only place that said so. A logos newer than its plugin
# is the ordinary state between an upgrade and the next session, which `plugin
# autoupdate` closes on its own and doctor reports, so it is not said here.
#
# Versions only, compared on this machine: asking a registry what is current
# would be a network call on every session start.
logos_skew_notice() {
  local plugin
  plugin=$(logos_plugin_version)
  if [ -n "$plugin" ] && [ -n "$LOGOS_RESOLVED_VERSION" ] && logos_newer "$plugin" "$LOGOS_RESOLVED_VERSION"; then
    # An unversioned build is not "older": logos_newer ranks it below
    # everything, and a developer's own checkout is the newest code there is.
    case "${LOGOS_RESOLVED_VERSION#v}" in
      [0-9]*) printf '%s\n' "This Claude Code plugin is Logos $plugin but the logos it runs ($LOGOS_RESOLVED) is ${LOGOS_RESOLVED_VERSION#v}, which ignores what newer hooks ask of it, so some of the plugin's features are off; upgrade with \`brew upgrade logos-mcp\` or \`npm i -g @noeton/logos@latest\`." ;;
    esac
  fi
  if [ -n "$LOGOS_PASSED_DEV" ]; then
    printf '%s\n' "The plugin runs $LOGOS_RESOLVED (${LOGOS_RESOLVED_VERSION#v}), not the dev build at $LOGOS_PASSED_DEV, because a dev build cannot say how old it is; set LOGOS_BIN=$LOGOS_PASSED_DEV in Claude Code's environment to run that one instead."
  fi
}

# logos_project echoes the project name for a directory, asking the binary
# rather than deciding in bash.
#
# The hooks used to say `basename "$PWD"`, which is only the *fallback* half of
# the rule: it cannot see a .logos-project marker, so a repository that renamed
# itself got one name from the MCP server and another from the hooks, and the
# handoff quietly stopped being found. One rule, implemented once, in Go.
#
# Falls back to the basename when the binary is too old to know the verb — an
# old binary prints usage to stderr and nothing usable to stdout, and a hook
# must not go silent over a version skew. Anything that is not a single clean
# token is treated as that case. A binary that succeeds and prints nothing is
# answering "no project" (the home directory, /), and that answer is kept:
# falling back there filed every session started in ~ under the user's name.
logos_project() {
  local dir="${1:-$PWD}" name
  name=$("${LOGOS[@]}" project-name "$dir" 2>/dev/null) || name=" "
  case "$name" in
    "") ;;
    *[[:space:]]*) basename "$dir" ;;
    *) printf '%s\n' "$name" ;;
  esac
}
