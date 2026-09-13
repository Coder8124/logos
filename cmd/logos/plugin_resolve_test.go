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
	cmd.Env = []string{"PATH=" + path, "HOME=" + t.TempDir(), "GOBIN=", "GOPATH="}
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
	if got != "brain" {
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
	if got != "brain" {
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
