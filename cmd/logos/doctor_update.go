package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/selfupdate"
)

// updateCheck is doctor's "version" row under --check-updates: whether this
// binary is behind the latest release, and the command that updates it the way
// it was installed.
//
// Behind a flag, never in a plain doctor: it asks GitHub, and nothing leaves
// the machine unless the user typed the command that needs it. It is the same
// one bare GET `logos update --check` makes, so `doctor --egress` still lists
// a single address for both.
//
// A failed request is Unknown, not Failed: being offline is not a broken
// install, and the doctor that refuses to pass on a train is one people stop
// running. It is still said, with the reason, rather than dropped.
func updateCheck(current string, opts selfupdate.Options, kind selfupdate.InstallKind) health.Check {
	c := health.Check{Name: "version"}
	if selfupdate.IsDevBuild(current) {
		c.State = health.Unknown
		c.Detail = "an unstamped dev build; there is no release to compare it with"
		return c
	}
	rel, err := selfupdate.CheckOnly(current, opts)
	if err != nil {
		c.State = health.Unknown
		c.Detail = fmt.Sprintf("logos %s; could not ask GitHub for the latest release: %v", current, err)
		return c
	}
	switch {
	case selfupdate.Newer(rel.Version, current):
		c.State = health.Warn
		c.Detail = fmt.Sprintf("logos %s is out of date; the latest release is %s", current, rel.Version)
		c.Fix = upgradeCommand(kind)
	case selfupdate.Newer(current, rel.Version):
		c.State = health.OK
		c.Detail = fmt.Sprintf("logos %s, newer than the latest release (%s)", current, rel.Version)
	default:
		// Compared as versions, not strings: the tag is "v0.5.8" and a
		// stamped binary may say "0.5.8".
		c.State = health.OK
		c.Detail = fmt.Sprintf("logos %s, the latest release", current)
	}
	return c
}

// upgradeCommand is the update that sticks for how this copy was installed.
// `logos update` on a Homebrew binary is undone by the next `brew upgrade`, and
// on an npm one leaves npm's own version metadata saying the old release.
func upgradeCommand(kind selfupdate.InstallKind) string {
	switch kind {
	case selfupdate.Homebrew:
		return "brew upgrade " + selfupdate.HomebrewFormula
	case selfupdate.NPMManaged:
		return "npm i -g @noeton/logos@latest"
	case selfupdate.NPX:
		return "npx @noeton/logos@latest, or install it: npm i -g @noeton/logos"
	}
	return "logos update"
}

// runningInstall is how the logos running now was installed. An unresolvable
// path is a standalone binary, whose fix, `logos update`, says for itself if it
// cannot replace anything.
func runningInstall() selfupdate.InstallKind {
	exe, err := os.Executable()
	if err != nil {
		return selfupdate.Standalone
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return selfupdate.DetectInstall(exe)
}
