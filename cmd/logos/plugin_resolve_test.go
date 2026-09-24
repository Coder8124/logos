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

// runResolver sources the plugin's resolve.sh under a PATH and HOME made of
// fakes only, and returns what it chose.
func runResolver(t *testing.T, path string, script string) (string, error) {
	t.Helper()
	resolver, err := filepath.Abs("../../plugin/bin/resolve.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", "-c", ". '"+resolver+"'; "+script)
	// No fixed install locations: the machine's own brew or /usr/local copy
	// would otherwise be a candidate in every test.
	cmd.Env = []string{"PATH=" + path, "HOME=" + t.TempDir(), "GOBIN=", "GOPATH=", "LOGOS_RESOLVE_FIXED_DIRS="}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Unrelated npm packages (`logos`, the "Logos module manager", and
// `logos-cli`) install a `logos` bin. The resolver took any `logos` on PATH, so
// the plugin would have run someone else's program as `logos mcp serve`.
func TestThePluginDoesNotRunSomeoneElsesLogos(t *testing.T) {
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `echo "Logos module manager 1.0.4"`)
	fakeProgram(t, bin, "brain", `echo "brain 0.4.3 darwin/arm64 go1.26"`)

	got, err := runResolver(t, bin+":/usr/bin:/bin", `logos_resolve && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	// Resolved by full path, not bare name: the resolver may deliberately pass
	// over the copy PATH would give, so running by name would launch the wrong
	// one (#157).
	if filepath.Base(got) != "brain" {
		t.Errorf("the resolver chose %q over this product's brain", got)
	}
}

// An npm global install puts a node shim on disk, and Claude Code launched
// from the Dock has no node on its PATH. The resolver accepted the shim because
// the file existed, and the server died with `env: node: No such file or
// directory` instead of the resolver moving on to a binary that works.
func TestThePluginSkipsACandidateThatDoesNotRun(t *testing.T) {
	shims, gobin := t.TempDir(), t.TempDir()
	fakeProgram(t, shims, "logos", `echo "env: node: No such file or directory" >&2; exit 127`)
	fakeProgram(t, gobin, "brain", `echo "brain 0.4.3 darwin/arm64 go1.26"`)

	got, err := runResolver(t, shims+":"+gobin+":/usr/bin:/bin", `logos_resolve && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if filepath.Base(got) != "brain" {
		t.Errorf("the resolver chose %q, which does not run", got)
	}
}

// When the only candidate does not run, the server must say that it found one
// and why it was passed over — "no binary found" sends a user with Logos
// installed off to install it again.
func TestThePluginSaysWhenTheBinaryItFoundDoesNotRun(t *testing.T) {
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		for _, name := range []string{"logos", "brain"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Skipf("%s/%s exists on this machine and would be resolved", dir, name)
			}
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

// Same version, two names: `brain` is pre-rename and so never newer than the
// `logos` it was renamed from.
func TestAtTheSameVersionLogosBeatsThePreRenameBrain(t *testing.T) {
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `echo "logos 0.4.7 darwin/arm64 go1.26"`)
	fakeProgram(t, bin, "brain", `echo "brain 0.4.7 darwin/arm64 go1.26"`)

	got, err := runResolver(t, bin+":/usr/bin:/bin",
		`logos_resolve 2>/dev/null && printf '%s\n' "${LOGOS[@]}"`)
	if err != nil {
		t.Fatalf("resolver failed: %v\n%s", err, got)
	}
	if filepath.Base(got) != "logos" {
		t.Errorf("the resolver chose %q at equal versions", got)
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
