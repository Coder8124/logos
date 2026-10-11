package scripts

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkCommit runs check-commits.sh over a throwaway repository holding one
// commit with msg, and reports whether the script passed it.
func checkCommit(t *testing.T, msg string) (bool, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "Base")
	git("commit", "-q", "--allow-empty", "-m", msg)

	script, err := filepath.Abs("check-commits.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "HEAD~1", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// "generated with" was matched anywhere in a body, so an ordinary sentence
// about go generate failed the commits job — and with it `ci ok`, the one
// check main's protection requires.
func TestABodyThatSaysGeneratedWithInPassingIsNotAttribution(t *testing.T) {
	for _, body := range []string{
		"The table is generated with go generate from the schema.",
		// Wrapped, the same sentence puts the phrase at the start of a line.
		"The docs table is regenerated from the schema, which is itself\ngenerated with go generate from the migrations.",
	} {
		ok, out := checkCommit(t, "The table is regenerated\n\n"+body)
		if !ok {
			t.Errorf("an ordinary body was rejected as attribution:\n%s", out)
		}
	}
}

func TestAnAttributionLineIsStillRejected(t *testing.T) {
	for _, body := range []string{
		"🤖 Generated with [Claude Code](https://claude.com/claude-code)",
		"Generated with Claude Code",
		"Co-Authored-By: Someone <someone@example.com>",
	} {
		ok, out := checkCommit(t, "A change\n\n"+body)
		if ok || !strings.Contains(out, "attribution") {
			t.Errorf("%q was let through:\n%s", body, out)
		}
	}
}
