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
# launched app does not inherit; on that machine `command -v brain` finds
# nothing while `brain` sits installed and working two directories away. The
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
# Dock. Every brain since the plugin existed answers --version with "brain …".

logos_runs() {
  case "$("$@" --version 2>/dev/null)" in
    "brain "*) return 0 ;;
  esac
  [ -n "$LOGOS_REJECTED" ] || LOGOS_REJECTED="$*"
  return 1
}

logos_resolve() {
  local name dir cand
  LOGOS_REJECTED=""
  for name in logos brain; do
    cand=$(command -v "$name" 2>/dev/null) || continue
    if logos_runs "$cand"; then
      LOGOS=("$name")
      return 0
    fi
  done
  for dir in "${GOBIN:-}" "${GOPATH:+$GOPATH/bin}" "$HOME/go/bin" \
             /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin"; do
    [ -n "$dir" ] || continue
    for name in logos brain; do
      cand="$dir/$name"
      if [ -x "$cand" ] && logos_runs "$cand"; then
        LOGOS=("$cand")
        return 0
      fi
    done
  done
  # Last resort: the published wrapper. Probed rather than assumed, so an
  # unpublished or offline registry fails here instead of at the first real
  # call, where it would look like Logos itself was broken.
  if command -v npx >/dev/null 2>&1 && npx -y @noeton/logos --version >/dev/null 2>&1; then
    LOGOS=(npx -y @noeton/logos)
    return 0
  fi
  return 1
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
# token is treated as that case.
logos_project() {
  local dir="${1:-$PWD}" name
  name=$("${LOGOS[@]}" project-name "$dir" 2>/dev/null) || name=""
  case "$name" in
    ""|*[[:space:]]*) basename "$dir" ;;
    *) printf '%s\n' "$name" ;;
  esac
}
