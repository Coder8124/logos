package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProgram writes an executable shell script named name into dir.
func fakeProgram(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// noFixedInstallDirs keeps resolve.sh off the machine's own Homebrew and
// /usr/local copies. Without it a test's fake logos competes with whatever
// release is installed there, and a newer one wins: the hook tests then ran the
// real binary and failed on any Mac with Logos installed, passing in CI only
// because the runners have none (#325, after #173 here).
const noFixedInstallDirs = "LOGOS_RESOLVE_FIXED_DIRS="

// runResolver sources the plugin's resolve.sh under a PATH and HOME made of
// fakes only, and returns what it chose.
func runResolver(t *testing.T, path string, script string) (string, error) {
	t.Helper()
	resolver, err := filepath.Abs("../../plugin/bin/resolve.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", "-c", ". '"+resolver+"'; "+script)
	cmd.Env = []string{"PATH=" + path, "HOME=" + t.TempDir(), "GOBIN=", "GOPATH=", noFixedInstallDirs}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Unrelated npm packages (`logos`, the "Logos module manager", and
// `logos-cli`) install a `logos` bin. The resolver took any `logos` on PATH, so
// the plugin would have run someone else's program as `logos mcp serve`.
func TestThePluginDoesNotRunSomeoneElsesLogos(t *testing.T) {
	other, ours := t.TempDir(), t.TempDir()
	fakeProgram(t, other, "logos", `echo "Logos module manager 1.0.4"`)
	fakeProgram(t, ours, "logos", `echo "logos 0.4.3 darwin/arm64 go1.26"`)

	got, err := runResolver(t, other+":"+ours+":/usr/bin:/bin", `logos_resolve && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	// Resolved by full path, not bare name: the resolver may deliberately pass
	// over the copy PATH would give, so running by name would launch the wrong
	// one (#157).
	if !strings.HasPrefix(got, ours) {
		t.Errorf("the resolver chose %q over this product's logos", got)
	}
}

// An npm global install puts a node shim on disk, and Claude Code launched
// from the Dock has no node on its PATH. The resolver accepted the shim because
// the file existed, and the server died with `env: node: No such file or
// directory` instead of the resolver moving on to a binary that works.
func TestThePluginSkipsACandidateThatDoesNotRun(t *testing.T) {
	shims, gobin := t.TempDir(), t.TempDir()
	fakeProgram(t, shims, "logos", `echo "env: node: No such file or directory" >&2; exit 127`)
	fakeProgram(t, gobin, "logos", `echo "logos 0.4.3 darwin/arm64 go1.26"`)

	got, err := runResolver(t, shims+":"+gobin+":/usr/bin:/bin", `logos_resolve && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if !strings.HasPrefix(got, gobin) {
		t.Errorf("the resolver chose %q, which does not run", got)
	}
}

// When the only candidate does not run, the server must say that it found one
// and why it was passed over — "no binary found" sends a user with Logos
// installed off to install it again.
func TestThePluginSaysWhenTheBinaryItFoundDoesNotRun(t *testing.T) {
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if _, err := os.Stat(filepath.Join(dir, "logos")); err == nil {
			t.Skipf("%s/logos exists on this machine and would be resolved", dir)
		}
	}
	shims := t.TempDir()
	fakeProgram(t, shims, "logos", `echo "env: node: No such file or directory" >&2; exit 127`)

	mcp, err := filepath.Abs("../../plugin/bin/mcp.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", mcp)
	cmd.Env = []string{"PATH=" + shims + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "GOBIN=", "GOPATH="}
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), filepath.Join(shims, "logos")) || !strings.Contains(string(out), "does not run") {
		t.Errorf("mcp.sh did not name the candidate it passed over:\n%s", out)
	}
}

// `brain project-name ~` prints nothing, which means no project, and the hook
// read that as an old binary and fell back to the folder name, so every session
// started in the home directory was filed under the user's name.
func TestThePluginKeepsTheBinarysAnswerOfNoProject(t *testing.T) {
	bin := t.TempDir()
	fakeProgram(t, bin, "global", `exit 0`)
	fakeProgram(t, bin, "old", `echo "unknown command" >&2; exit 1`)
	got, err := runResolver(t, "/usr/bin:/bin", "LOGOS=('"+filepath.Join(bin, "global")+"'); logos_project /Users/someone")
	if err != nil || got != "" {
		t.Errorf("an empty answer from the binary became %q (err %v), want no project", got, err)
	}
	got, _ = runResolver(t, "/usr/bin:/bin", "LOGOS=('"+filepath.Join(bin, "old")+"'); logos_project /Users/someone")
	if got != "someone" {
		t.Errorf("a binary without the verb gave %q, want the basename fallback %q", got, "someone")
	}
}

// The only install the message offered needed a Go toolchain, which a user who
// installed Claude Code from its own native installer is even less likely to
// have than Node. A user with neither is told to fetch a third toolchain for a
// binary that Homebrew or npm would have handed them directly.
func TestThePluginOffersAnInstallThatNeedsNoToolchain(t *testing.T) {
	// A copy of the script beside a resolver that finds nothing: the real one
	// searches absolute directories, so on a machine with Logos installed the
	// message under test is unreachable.
	bin := t.TempDir()
	real, err := os.ReadFile("../../plugin/bin/mcp.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mcp.sh"), real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "resolve.sh"), []byte("logos_resolve() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/bash", filepath.Join(bin, "mcp.sh"))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	out, _ := cmd.CombinedOutput()
	got := string(out)
	if !strings.Contains(got, "brew install coder8124/tap/logos-mcp") {
		t.Errorf("the message never offers Homebrew, the route that needs no toolchain:\n%s", got)
	}
	if !strings.Contains(got, "npm i -g @noeton/logos") {
		t.Errorf("the message never offers npm, the route for Windows and for a machine with Node:\n%s", got)
	}
	// Go stays, last: it is the right answer for whoever already has it.
	if brew, goInstall := strings.Index(got, "brew install"), strings.Index(got, "go install"); goInstall >= 0 && goInstall < brew {
		t.Errorf("go install is offered before the routes that need no toolchain:\n%s", got)
	}
}

// The resolver took the first working candidate, never comparing versions, so
// a `go install` build from months ago sitting earlier on PATH than a fresh
// `brew upgrade` won every time. That is not one stale feature: every guard
// the hooks rely on is an environment variable an older binary ignores, so the
// plugin silently reverted to whatever that binary did — #145's ghost projects
// were this, and nothing on screen said which copy answered.
func TestThePluginRunsTheNewestLogosNotTheFirstOneFound(t *testing.T) {
	stale, fresh := t.TempDir(), t.TempDir()
	fakeProgram(t, stale, "logos", `echo "logos 0.4.1 darwin/arm64 go1.26"`)
	fakeProgram(t, fresh, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)

	got, err := runResolver(t, stale+":"+fresh+":/usr/bin:/bin",
		`logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if !strings.HasPrefix(got, fresh) {
		t.Errorf("the resolver chose %q, not the newer copy in %s", got, fresh)
	}
}

// Invariant 3: a choice the user cannot see is the whole of this bug. When the
// plugin runs something other than the copy the user gets by typing `logos`,
// it says so, with both versions.
func TestThePluginSaysWhenItPassesOverTheLogosOnPath(t *testing.T) {
	stale, fresh := t.TempDir(), t.TempDir()
	fakeProgram(t, stale, "logos", `echo "logos 0.4.1 darwin/arm64 go1.26"`)
	fakeProgram(t, fresh, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)

	got, err := runResolver(t, stale+":"+fresh+":/usr/bin:/bin", `logos_resolve`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if !strings.Contains(got, "0.4.7") || !strings.Contains(got, "0.4.1") {
		t.Errorf("the resolver passed over the logos on PATH without saying so: %q", got)
	}
}

// An unversioned build — what `go install` produces — cannot say how old it is,
// and the failure that shipped is exactly one of those beating a current
// install. A numbered release wins.
func TestANumberedReleaseBeatsAnUnversionedBuild(t *testing.T) {
	dev, release := t.TempDir(), t.TempDir()
	fakeProgram(t, dev, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)

	got, err := runResolver(t, dev+":"+release+":/usr/bin:/bin",
		`logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if !strings.HasPrefix(got, release) {
		t.Errorf("the resolver kept the unversioned build %q over a numbered release", got)
	}
}

// A developer running their own build needs a way to say so out loud, or the
// version ranking above makes this repository impossible to dogfood.
func TestAnExplicitLogosBinIsNotRankedAgainstAnythingElse(t *testing.T) {
	mine, release := t.TempDir(), t.TempDir()
	fakeProgram(t, mine, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)

	got, err := runResolver(t, release+":/usr/bin:/bin",
		`LOGOS_BIN=`+filepath.Join(mine, "logos")+` logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if !strings.HasPrefix(got, mine) {
		t.Errorf("LOGOS_BIN was overridden by version ranking: chose %q", got)
	}
}

// A pre-rename brain reads only the BRAIN_ names the hooks no longer set, so
// it would run with every guard off — even when it is the newer copy.
func TestThePluginNoLongerRunsAPreRenameBrain(t *testing.T) {
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)
	fakeProgram(t, bin, "brain", `echo "brain 0.4.9 darwin/arm64 go1.26"`)

	got, err := runResolver(t, bin+":/usr/bin:/bin",
		`logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if filepath.Base(got) != "logos" {
		t.Errorf("the resolver chose %q over logos", got)
	}
}

// #173: a version with a leading v was ranked as an unversioned dev build,
// older than everything. `go install …@v0.4.9` reports v0.4.9, and a release
// archive is stamped from the tag, v included — so a v0.4.9 lost to 0.4.8, and
// two tagged releases never displaced each other at all.
func TestAVersionWrittenWithAVIsRankedByItsNumber(t *testing.T) {
	for _, c := range []struct{ older, newer string }{
		{"0.4.8", "v0.4.9"},
		{"v0.4.1", "v0.4.8"},
		{"v0.4.8", "0.4.9"},
	} {
		stale, fresh := t.TempDir(), t.TempDir()
		fakeProgram(t, stale, "logos", `echo "logos `+c.older+` darwin/arm64 go1.26"`)
		fakeProgram(t, fresh, "logos", `echo "logos `+c.newer+` darwin/arm64 go1.26"`)

		got, err := runResolver(t, stale+":"+fresh+":/usr/bin:/bin",
			`logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
		if err != nil {
			t.Fatalf("resolver failed: %v\n%s", err, got)
		}
		if !strings.HasPrefix(got, fresh) {
			t.Errorf("%s was kept over %s", c.older, c.newer)
		}
	}
}

// skewNotice resolves on path with the plugin stated as 0.5.6, whatever this
// checkout's manifest says, and returns what was picked, the line meant for
// the agent to relay (stdout) and the detail kept on stderr. setup runs first,
// in the same shell, for an exported LOGOS_BIN.
func skewNotice(t *testing.T, path, setup string) (picked, relay, detail string) {
	t.Helper()
	got, err := runResolver(t, path, setup+`
logos_plugin_version() { echo 0.5.6; }
logos_resolve 2>/dev/null || exit 3
printf '%s\n' "${LOGOS[*]}"
logos_skew_notice 2>/dev/null
echo ---detail---
logos_skew_notice 2>&1 >/dev/null`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	picked, rest, _ := strings.Cut(got, "\n")
	relay, detail, _ = strings.Cut(rest, "---detail---")
	return picked, strings.TrimSpace(relay), strings.TrimSpace(detail)
}

// #342: a dev build beside an older numbered release lost the ranking, and
// the only word of it was a stderr line Claude Code never shows — so a
// developer ran the old copy and took its bugs for their own. The resolver
// still picks the release (a dev build cannot say how old it is), but the
// relayed line now says a dev build was passed over and gives the command
// that runs it.
func TestADevBuildPassedOverForAnOlderReleaseIsAnnounced(t *testing.T) {
	dev, release := t.TempDir(), t.TempDir()
	fakeProgram(t, dev, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.5.1 darwin/arm64 go1.26"`)
	devBin, releaseBin := filepath.Join(dev, "logos"), filepath.Join(release, "logos")

	picked, relay, detail := skewNotice(t, dev+":"+release+":/usr/bin:/bin", "")
	if picked != releaseBin {
		t.Errorf("picked %q, want the numbered release %q", picked, releaseBin)
	}
	for _, want := range []string{"dev build", "Logos 0.5.1", "LOGOS_BIN='" + devBin + "'"} {
		if !strings.Contains(relay, want) {
			t.Errorf("the relayed line never said %q:\n%s", want, relay)
		}
	}
	if !strings.Contains(detail, releaseBin) {
		t.Errorf("stderr never named the binary that runs:\n%s", detail)
	}

	// Chosen on purpose, the dev build is neither passed over nor "older".
	_, relay, _ = skewNotice(t, release+":/usr/bin:/bin", "export LOGOS_BIN="+devBin)
	if relay != "" {
		t.Errorf("an explicit LOGOS_BIN dev build was still announced:\n%s", relay)
	}
}

// `logos plugin autoupdate` moves the plugin ahead of brew on every patch
// release, and hook flags change only with a minor one, so a patch gap told
// the user every session that features were off and to run a brew upgrade that
// had nothing to give them yet. It is not relayed; stderr keeps a soft line.
func TestAPatchReleaseBehindItsPluginIsNotCalledOutOfDate(t *testing.T) {
	release := t.TempDir()
	fakeProgram(t, release, "logos", `echo "logos 0.5.5 darwin/arm64 go1.26"`)
	_, relay, detail := skewNotice(t, release+":/usr/bin:/bin", "")
	if relay != "" {
		t.Errorf("a patch release behind the plugin was relayed:\n%s", relay)
	}
	if strings.Contains(detail, "features are off") || !strings.Contains(detail, "0.5.5") {
		t.Errorf("stderr should say softly that 0.5.5 is a patch behind 0.5.6:\n%s", detail)
	}
}

// A LOGOS_BIN the user set is not fixed by `brew upgrade`: that upgrades a copy
// LOGOS_BIN tells the plugin to ignore.
func TestAnOldLogosBinIsToldToUpdateOrUnsetIt(t *testing.T) {
	mine := t.TempDir()
	fakeProgram(t, mine, "logos", `echo "logos 0.4.9 darwin/arm64 go1.26"`)
	_, relay, _ := skewNotice(t, "/usr/bin:/bin", "export LOGOS_BIN="+filepath.Join(mine, "logos"))
	if !strings.Contains(relay, "0.4.9") || !strings.Contains(relay, "unset LOGOS_BIN") {
		t.Errorf("an old LOGOS_BIN was not told to update or unset LOGOS_BIN:\n%s", relay)
	}
	if strings.Contains(relay, "brew upgrade") {
		t.Errorf("an old LOGOS_BIN was told to upgrade a copy LOGOS_BIN overrides:\n%s", relay)
	}
}

// When the old copy won because the install beside it does not run — an npm
// shim with no node on Claude Code's PATH — upgrading changes nothing: the
// upgraded copy fails the same way. The one that does not run is what to fix.
func TestAnOldLogosChosenBecauseAnotherDidNotRunNamesTheOneThatDidNot(t *testing.T) {
	broken, old := t.TempDir(), t.TempDir()
	fakeProgram(t, broken, "logos", `echo "env: node: No such file or directory" >&2; exit 127`)
	fakeProgram(t, old, "logos", `echo "logos 0.4.9 darwin/arm64 go1.26"`)
	_, relay, _ := skewNotice(t, broken+":"+old+":/usr/bin:/bin", "")
	if !strings.Contains(relay, filepath.Join(broken, "logos")) || !strings.Contains(relay, "does not run") {
		t.Errorf("the install that does not run was not named:\n%s", relay)
	}
	if strings.Contains(relay, "brew upgrade") {
		t.Errorf("an upgrade was advised for a copy that cannot run here:\n%s", relay)
	}
}

// A LOGOS_BIN that does not run fell through to the ranking with nothing said,
// and the dev-build notice then told the user to set the LOGOS_BIN they had
// already set.
func TestALogosBinThatDoesNotRunIsAnnounced(t *testing.T) {
	dev, release := t.TempDir(), t.TempDir()
	fakeProgram(t, dev, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.5.6 darwin/arm64 go1.26"`)
	_, relay, detail := skewNotice(t, dev+":"+release+":/usr/bin:/bin", "export LOGOS_BIN=/nowhere/logos")
	if !strings.Contains(relay, "LOGOS_BIN does not run") {
		t.Errorf("a LOGOS_BIN that does not run was passed over in silence:\n%s", relay)
	}
	if strings.Contains(relay, "set LOGOS_BIN") {
		t.Errorf("the user was told to set the LOGOS_BIN they already set:\n%s", relay)
	}
	if !strings.Contains(detail, "/nowhere/logos") {
		t.Errorf("stderr never named the LOGOS_BIN that was rejected:\n%s", detail)
	}
}

// The npx fallback ran whatever npx had cached, and set nothing the notice
// reads, so a months-old cached package was never announced.
func TestACachedNpxLogosOlderThanItsPluginIsAnnounced(t *testing.T) {
	tools := t.TempDir()
	fakeProgram(t, tools, "npx", `echo "logos 0.4.9 darwin/arm64 go1.26"`)
	picked, relay, _ := skewNotice(t, tools+":/usr/bin:/bin", "")
	if !strings.HasPrefix(picked, "npx") {
		t.Fatalf("picked %q, want the npx fallback", picked)
	}
	if !strings.Contains(relay, "0.4.9") || !strings.Contains(relay, "npx") || !strings.Contains(relay, "npm i -g @noeton/logos@latest") {
		t.Errorf("an old cached npx copy was not announced with its fix:\n%s", relay)
	}
}

// Only a shim that cannot start — exit 127, `env: node: No such file` — is an
// install of ours that does not run. Someone else's `logos` (the npm "Logos
// module manager") also fails logos_runs, and was diagnosed every session as
// "an npm install with no node", in place of the upgrade that fixes it.
func TestSomeoneElsesLogosBesideAnOldReleaseKeepsTheUpgradeAdvice(t *testing.T) {
	other, brew := t.TempDir(), t.TempDir()
	fakeProgram(t, other, "logos", `echo "Logos module manager 1.0.4"`)
	fakeProgram(t, brew, "logos", `echo "logos 0.4.9 darwin/arm64 go1.26"`)
	_, relay, _ := skewNotice(t, other+":"+brew+":/usr/bin:/bin", "")
	if !strings.Contains(relay, "brew upgrade logos-mcp") {
		t.Errorf("an old release beside someone else's logos lost its upgrade advice:\n%s", relay)
	}
	if strings.Contains(relay, other) || strings.Contains(relay, "does not run") {
		t.Errorf("someone else's logos was diagnosed as a broken install of ours:\n%s", relay)
	}
}

// The suggested commands are pasted into a shell or a settings file, and a
// path with a space in it was printed bare, so the pasted line split in two.
func TestASuggestedPathWithASpaceIsQuotedToPaste(t *testing.T) {
	root := t.TempDir()
	dev, release, broken, old := filepath.Join(root, "dev dir"), filepath.Join(root, "rel"), filepath.Join(root, "npm dir"), filepath.Join(root, "old")
	for _, d := range []string{dev, release, broken, old} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeProgram(t, dev, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.5.6 darwin/arm64 go1.26"`)
	fakeProgram(t, broken, "logos", `echo "env: node: No such file or directory" >&2; exit 127`)
	fakeProgram(t, old, "logos", `echo "logos 0.4.9 darwin/arm64 go1.26"`)

	_, relay, _ := skewNotice(t, dev+":"+release+":/usr/bin:/bin", "")
	if want := "LOGOS_BIN='" + filepath.Join(dev, "logos") + "'"; !strings.Contains(relay, want) {
		t.Errorf("the dev build's path was not quoted to paste, want %s in:\n%s", want, relay)
	}
	_, relay, _ = skewNotice(t, broken+":"+old+":/usr/bin:/bin", "")
	if want := "'" + filepath.Join(broken, "logos") + "'"; !strings.Contains(relay, want) {
		t.Errorf("the install that does not run was not quoted, want %s in:\n%s", want, relay)
	}
}

// A dev build passed over may be the newest code on the machine or a
// months-old `go install` nobody remembers, and the resolver cannot tell
// which. The line told everyone to switch to it — the stale copy too — and
// read "passed over your dev build for Logos 0.4.9" as if 0.4.9 were its
// version. It now offers both ways out and says which copy runs.
func TestADevBuildPassedOverIsOfferedEitherWayOut(t *testing.T) {
	dev, release := t.TempDir(), t.TempDir()
	fakeProgram(t, dev, "logos", `echo "logos dev darwin/arm64 go1.26.5"`)
	fakeProgram(t, release, "logos", `echo "logos 0.5.6 darwin/arm64 go1.26"`)
	_, relay, _ := skewNotice(t, dev+":"+release+":/usr/bin:/bin", "")
	for _, want := range []string{"runs Logos 0.5.6", "LOGOS_BIN=", "delete"} {
		if !strings.Contains(relay, want) {
			t.Errorf("the dev-build line never said %q:\n%s", want, relay)
		}
	}
	if strings.Contains(relay, "for Logos") {
		t.Errorf("the dev-build line still reads as if the release were its version:\n%s", relay)
	}
}

// "0.5.6-rc.1" ranked above 0.5.6: the suffix was stripped field by field, so
// rc.1's ".1" was read as a fourth number. A pre-release is below its release.
func TestAPreReleaseRanksBelowItsRelease(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"0.5.6", "0.5.6-rc.1", true},
		{"0.5.6-rc.1", "0.5.6", false},
		{"v0.5.6-rc.1", "0.5.6", false},
		{"0.5.6-rc.1", "0.5.5", true},
		{"0.5.6-rc.1", "0.5.6-rc.1", false},
		{"0.5.6+build.7", "0.5.6", false},
	} {
		got, _ := runResolver(t, "/usr/bin:/bin", `logos_newer "`+c.a+`" "`+c.b+`" && echo yes || echo no`)
		if (got == "yes") != c.newer {
			t.Errorf("logos_newer %s %s = %s, want newer=%v", c.a, c.b, got, c.newer)
		}
	}
}
