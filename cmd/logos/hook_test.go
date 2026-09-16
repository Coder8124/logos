package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLogos is a logos whose `resume` prints what the test wants it to, so the
// hook can be exercised without a vault behind it.
func fakeLogos(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	fakeProgram(t, dir, "logos", body)
	return filepath.Join(dir, "logos")
}

// A checkpoint nobody is asked for is a checkpoint nobody reads. Cursor and
// Codex run a session-start hook, and each one names the field it injects
// context through differently, so one shape would have been injected by neither.
func TestTheHookHandsEachHostContextInTheFieldThatHostReads(t *testing.T) {
	out := hookOutput("cursor", "## Where we left off\nthe parser")
	var cursor map[string]string
	if err := json.Unmarshal([]byte(out), &cursor); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out)
	}
	if !strings.Contains(cursor["additional_context"], "the parser") {
		t.Errorf("Cursor gets nothing it reads: %s", out)
	}

	out = hookOutput("codex", "## Where we left off\nthe parser")
	var codex struct {
		Specific struct {
			Additional string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &codex); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, out)
	}
	if !strings.Contains(codex.Specific.Additional, "the parser") {
		t.Errorf("Codex gets nothing it reads: %s", out)
	}
}

// A hook that prints on every session, with nothing to say, spends the user's
// context on a heading. Only an actual handoff is worth injecting.
func TestTheHookSaysNothingWhenThereIsNoHandoff(t *testing.T) {
	if out := hookOutput("cursor", ""); out != "" {
		t.Errorf("the hook printed with nothing to say: %q", out)
	}
	if out := hookOutput("nosuchhost", "## Where we left off\nx"); out != "" {
		t.Errorf("an unknown host got output shaped for someone else: %q", out)
	}
}

// resume answers with standing memories and notes even when no session has ever
// been checkpointed here. That is useful to a person reading it and noise to a
// model that has just been handed the repository.
func TestOnlyAnActualHandoffIsInjected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOGOS_PROJECT", "shop")

	bare := fakeLogos(t, `echo "## What matters here"; echo "- a memory"`)
	if got := handoffFrom(bare, dir); got != "" {
		t.Errorf("a resume with no checkpoint was injected anyway:\n%s", got)
	}

	withHandoff := fakeLogos(t, `echo "## What matters here"; echo "## Where we left off"; echo "the parser"`)
	if got := handoffFrom(withHandoff, dir); !strings.Contains(got, "the parser") {
		t.Errorf("the handoff was not injected:\n%s", got)
	}
}

// Invariant: a hook never fails. A logos that is missing, broken or slow makes
// the session start with less context, never with an error in the host's face.
func TestAHookWhoseLogosFailsIsSilentRatherThanLoud(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOGOS_PROJECT", "shop")

	broken := fakeLogos(t, `echo "boom" >&2; exit 3`)
	// Otherwise the hook would run the test binary as if it were logos.
	was := executable
	executable = func() (string, error) { return broken, nil }
	defer func() { executable = was }()
	if got := handoffFrom(broken, dir); got != "" {
		t.Errorf("a failed resume was injected: %q", got)
	}
	if err := runInDir(t, dir, func() error { return hookCmd([]string{"cursor", "session-start"}) }); err != nil {
		t.Errorf("the hook reported a failure at the host: %v", err)
	}
	// An event this logos does not implement is the same case: the host asked,
	// and the honest answer is nothing, not a non-zero exit.
	if err := hookCmd([]string{"cursor", "some-future-event"}); err != nil {
		t.Errorf("an unknown event failed: %v", err)
	}
}

func runInDir(t *testing.T, dir string, fn func() error) error {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(was)
	return fn()
}
