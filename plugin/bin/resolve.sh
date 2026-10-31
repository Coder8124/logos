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
  LOGOS_RUN_STATUS=$?
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
#
# A pre-release ranks below its release, and build metadata (+…) not at all.
# The suffix used to be stripped field by field, so "0.5.6-rc.1" read its ".1"
# as a fourth number and outranked 0.5.6 — an old release candidate winning
# over the release that replaced it.
logos_newer() {
  local a="${1#v}" b="${2#v}" x y apre="" bpre=""
  case "$a" in [0-9]*) ;; *) return 1 ;; esac
  case "$b" in [0-9]*) ;; *) return 0 ;; esac
  a=${a%%+*}; b=${b%%+*}
  case "$a" in *-*) apre=1; a=${a%%-*} ;; esac
  case "$b" in *-*) bpre=1; b=${b%%-*} ;; esac
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
  # The same release: the final one is newer than any of its candidates.
  [ -z "$apre" ] && [ -n "$bpre" ]
}

logos_resolve() {
  local name dir cand best_ver="" best_desc="" first_desc="" dev_desc=""
  LOGOS_REJECTED=""
  # What won and what it reported, for logos_skew_notice. Kept apart from
  # LOGOS_VERSION, which is whatever candidate was probed last.
  # LOGOS_BIN_REJECTED and LOGOS_PASSED_BROKEN are the two ways the copy that
  # runs is not the one the user meant: the advice for an old binary depends on
  # which, because `brew upgrade` fixes neither.
  LOGOS_RESOLVED="" LOGOS_RESOLVED_VERSION="" LOGOS_PASSED_DEV=""
  LOGOS_BIN_REJECTED="" LOGOS_PASSED_BROKEN=""

  # An explicit choice is not a candidate to be ranked against others.
  if [ -n "${LOGOS_BIN:-}" ]; then
    if logos_runs "$LOGOS_BIN"; then
      LOGOS=("$LOGOS_BIN")
      LOGOS_RESOLVED="$LOGOS_BIN" LOGOS_RESOLVED_VERSION="$LOGOS_VERSION"
      return 0
    fi
    # Falling through is right — a session with some logos beats one with
    # none — but in silence it read as LOGOS_BIN being obeyed.
    LOGOS_BIN_REJECTED="$LOGOS_BIN"
  fi

  logos_consider() {
    local runner="$1" desc="$2"
    if ! logos_runs "$runner"; then
      # Only a copy that could not start: 127 is what env and the kernel give
      # an npm shim whose node is missing. Someone else's `logos` (see the top
      # of this file) also fails logos_runs, but naming it as our install with
      # no node replaced the upgrade advice that would have fixed things.
      [ "$LOGOS_RUN_STATUS" = 127 ] && [ -z "$LOGOS_PASSED_BROKEN" ] && LOGOS_PASSED_BROKEN="$desc"
      return 1
    fi
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
  #
  # The version is kept like any other winner's: npx runs whatever it cached,
  # however old, and with nothing recorded the skew notice never saw it. Not
  # through logos_runs, whose LOGOS_REJECTED would then name npx to mcp.sh in
  # place of the installed copy that really failed.
  local out
  if command -v npx >/dev/null 2>&1 && out=$(npx --prefer-offline -y @noeton/logos --version 2>/dev/null); then
    case "$out" in
      "logos "*)
        LOGOS=(npx --prefer-offline -y @noeton/logos)
        LOGOS_RESOLVED="npx" LOGOS_RESOLVED_VERSION=${out#* }
        LOGOS_RESOLVED_VERSION=${LOGOS_RESOLVED_VERSION%% *}
        return 0
        ;;
    esac
  fi
  return 1
}

# logos_minor_of echoes the major.minor of a version, without its v.
logos_minor_of() {
  local v="${1#v}" major
  major=${v%%.*}
  case "$v" in
    *.*) v=${v#*.}; printf '%s.%s\n' "$major" "${v%%.*}" ;;
    *) printf '%s\n' "$major" ;;
  esac
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

# logos_quote echoes a path in single quotes, so a suggested command pastes
# into a shell intact even when the path has a space or a quote in it.
logos_quote() {
  local q="'\\''"
  printf "'%s'" "${1//\'/$q}"
}

# logos_skew_notice echoes, on stdout, what the user needs to hear about the
# binary logos_resolve chose — one short sentence per problem, with the command
# that fixes it — or nothing. The paths and reasons behind it go to stderr,
# which Claude Code drops for a hook that exits 0: they are for whoever runs
# the hook or the resolver by hand to see why. Called once, from the
# SessionStart hook, which relays stdout as "one short line"; record.sh runs on every tool
# call and must not pay for it.
#
# The direction that matters is a logos a minor release older than its plugin.
# Every guard the hooks rely on is an argument or a variable, and "a logos too
# old for the flag" ignores it — so the plugin's newer features switched off one
# by one with the hooks' own comments the only place that said so. Those flags
# change only with a minor release, and `logos plugin autoupdate` moves the
# plugin ahead of brew on every patch, so a patch gap gets a soft stderr line
# and no relay: said every session, it would teach the user to skip the line.
# A logos newer than its plugin is the ordinary state between an upgrade and
# the next session, which autoupdate closes and doctor reports.
#
# The advice follows what chose the binary, because `brew upgrade` fixes only
# the ranking's own pick: a LOGOS_BIN overrides the upgraded copy, a broken
# install beside an old one fails the same way upgraded, and an npx cache is
# replaced by installing.
#
# Versions only, compared on this machine: asking a registry what is current
# would be a network call on every session start.
logos_skew_notice() {
  local plugin have
  plugin=$(logos_plugin_version)
  have=${LOGOS_RESOLVED_VERSION#v}
  if [ -n "$LOGOS_BIN_REJECTED" ]; then
    printf '%s\n' "LOGOS_BIN does not run as Logos, so the plugin uses another copy: fix that path or remove it from Claude Code's environment."
    printf 'logos: LOGOS_BIN=%s does not run as Logos; running %s (%s) instead\n' "$LOGOS_BIN_REJECTED" "${LOGOS_RESOLVED:-nothing}" "${have:-no version}" >&2
  fi
  # An unversioned build is not "older": logos_newer ranks it below
  # everything, and a developer's own checkout is the newest code there is.
  case "$have" in [0-9]*) ;; *) have="" ;; esac
  if [ -n "$plugin" ] && [ -n "$have" ] && logos_newer "$plugin" "$have"; then
    if logos_newer "$(logos_minor_of "$plugin")" "$(logos_minor_of "$have")"; then
      if [ -n "${LOGOS_BIN:-}" ] && [ "$LOGOS_RESOLVED" = "$LOGOS_BIN" ]; then
        printf '%s\n' "Logos $have from LOGOS_BIN is older than its $plugin plugin, so some plugin features are off: update that build or unset LOGOS_BIN."
      elif [ -n "$LOGOS_PASSED_BROKEN" ]; then
        printf '%s\n' "Logos $have is older than its $plugin plugin, so some plugin features are off: $(logos_quote "$LOGOS_PASSED_BROKEN") does not run here — an npm install with no node on Claude Code's PATH?"
      elif [ "$LOGOS_RESOLVED" = "npx" ]; then
        printf '%s\n' "Logos $have, cached by npx, is older than its $plugin plugin, so some plugin features are off: \`npm i -g @noeton/logos@latest\`."
      else
        printf '%s\n' "Logos $have is older than its $plugin plugin, so some plugin features are off: \`brew upgrade logos-mcp\` or \`npm i -g @noeton/logos@latest\`."
      fi
      printf 'logos: the plugin is %s and the logos it runs, %s, is %s\n' "$plugin" "$LOGOS_RESOLVED" "$have" >&2
    else
      printf 'logos: the plugin is %s and %s is %s, a patch apart; nothing the hooks ask needs the newer one, and `brew upgrade logos-mcp` closes it once brew has the release\n' "$plugin" "$LOGOS_RESOLVED" "$have" >&2
    fi
  fi
  # Never told to set a LOGOS_BIN they already set: the rejection above is
  # the thing to fix first.
  if [ -n "$LOGOS_PASSED_DEV" ] && [ -z "$LOGOS_BIN_REJECTED" ]; then
    # Both ways out, because the resolver cannot tell the newest code on the
    # machine from a months-old `go install`: telling everyone to switch sent
    # the stale copy's owner to it every session.
    printf '%s\n' "The plugin runs Logos ${LOGOS_RESOLVED_VERSION#v}, not your dev build: set LOGOS_BIN=$(logos_quote "$LOGOS_PASSED_DEV") to run it, or delete it if it is stale."
  fi
  if [ -n "$LOGOS_PASSED_DEV" ]; then
    printf 'logos: running %s (%s), not the dev build at %s, because a dev build cannot say how old it is\n' "$LOGOS_RESOLVED" "${LOGOS_RESOLVED_VERSION#v}" "$LOGOS_PASSED_DEV" >&2
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
