package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// `logos resume <project>` is the first command SETUP.md tells a new user to
// run, and until this was fixed a brand-new vault answered it with the context
// pack's empty rendering: a heading, a provenance disclaimer addressed to a
// model, a "Nothing recorded" section and a token budget. Nothing in that is
// wrong, and nothing in it is for a person — it reads as a failed install on
// the one command that is supposed to prove the install worked.
func TestResumingAnEmptyVaultSaysWhatToTypeNext(t *testing.T) {
	vaultDir := t.TempDir()

	out := captureStdout(t, func() { printNothingToResume(vaultDir, "myproj") })

	if strings.Contains(out, "Context budget") || strings.Contains(out, "## ") {
		t.Errorf("a new user's first command still prints the model-facing pack:\n%s", out)
	}
	// The two commands that make the next resume return something.
	for _, want := range []string{"logos note myproj", "logos checkpoint myproj"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not tell the user to run %q:\n%s", want, out)
		}
	}
	// An agent reading the same output over the CLI still needs the instruction
	// not to fill the gap from its own context.
	if !strings.Contains(out, "rather than inferring") {
		t.Errorf("the agent-facing warning was lost:\n%s", out)
	}
}

// A name that does not match, on a vault that has other work in it, is a
// different problem with a different answer: it is almost always a typo, and
// the fix is the list of names that would have worked.
func TestResumingAnUnknownProjectListsTheOnesThatExist(t *testing.T) {
	vaultDir := t.TempDir()
	for _, p := range []string{"logos", "kestrel"} {
		if err := os.MkdirAll(filepath.Join(vaultDir, session.CheckpointDir, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() { printNothingToResume(vaultDir, "logus") })

	if !strings.Contains(out, "kestrel") || !strings.Contains(out, "logos") {
		t.Errorf("a mistyped project name does not get the list of real ones:\n%s", out)
	}
	// Telling someone with a populated vault to start their first note is
	// wrong, and worse, it suggests the vault lost what is in it.
	if strings.Contains(out, "no checkpoints") {
		t.Errorf("a vault with checkpoints was described as having none:\n%s", out)
	}
}

// The failure this prevents is not an empty vault: it is two vaults. The MCP
// server is pinned to one with LOGOS_VAULT in the host config, the SessionStart
// hook inherits no such variable and falls to the recorded pointer, and a
// session then restores from a vault that nothing has ever checkpointed into
// while the real one fills up a directory away. "This vault has no checkpoints"
// is true of whichever vault was read, and naming it is what makes the split
// visible instead of reading as lost work.
func TestResumingAnEmptyVaultNamesTheVaultItRead(t *testing.T) {
	vaultDir := t.TempDir()

	out := captureStdout(t, func() { printNothingToResume(vaultDir, "myproj") })

	if !strings.Contains(out, vaultDir) {
		t.Errorf("nothing said which vault was read, so a session restoring from the wrong one cannot tell:\n%s", out)
	}
}

// Same reason on the other branch: a name that did not match is also the shape
// a wrong vault takes, when that vault happens to hold somebody else's work.
func TestResumingAnUnknownProjectNamesTheVaultItRead(t *testing.T) {
	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, session.CheckpointDir, "kestrel"), 0o700); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { printNothingToResume(vaultDir, "logus") })

	if !strings.Contains(out, vaultDir) {
		t.Errorf("nothing said which vault was searched:\n%s", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	fn()
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
