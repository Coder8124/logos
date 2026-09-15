package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hookEvent feeds one Claude Code hook payload through `logos activity record`
// as the plugin does, and returns what it printed on stdout.
func hookEvent(t *testing.T, event, payload string, extra ...string) string {
	t.Helper()
	restore := setStdin(t, []byte(payload))
	defer restore()
	return captureStdout(t, func() {
		args := append([]string{"--event", event, "--project", "shop"}, extra...)
		if err := recordActivity(args); err != nil {
			t.Fatalf("record %s: %v", event, err)
		}
	})
}

func stopHook(t *testing.T, payload string) string {
	t.Helper()
	return hookEvent(t, "Stop", payload, "--ask-checkpoint")
}

// A session that edited files and ended without a checkpoint carried nothing
// to the next one, and the user was told it had. Many hosts never show hook
// output, so the only fix that reaches them is asking the model itself before
// it stops — once, so a model that declines is not trapped in a loop.
func TestAStopAfterUncheckpointedWorkAsksTheAgentToCheckpointOnce(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	hookEvent(t, "UserPromptSubmit", `{"session_id":"s-work","prompt":"fix the checkout crash when cart is empty"}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-work","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)

	out := stopHook(t, `{"session_id":"s-work","stop_hook_active":false}`)
	if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "checkpoint") || !strings.Contains(out, "failed") {
		t.Fatalf("a stop after uncheckpointed edits did not ask for a checkpoint with failed filled in:\n%s", out)
	}
	if again := stopHook(t, `{"session_id":"s-work","stop_hook_active":false}`); strings.TrimSpace(again) != "" {
		t.Errorf("the second stop in the same session was blocked again:\n%s", again)
	}
}

// Claude Code sets stop_hook_active while the model is already continuing
// because a stop hook blocked; blocking then is how a hook loops forever.
func TestAStopTheHostSaysIsAlreadyContinuingIsNotBlocked(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	hookEvent(t, "PostToolUse", `{"session_id":"s-active","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)

	if out := stopHook(t, `{"session_id":"s-active","stop_hook_active":true}`); strings.TrimSpace(out) != "" {
		t.Errorf("a stop with stop_hook_active was blocked:\n%s", out)
	}
}

// A quick question answered by reading a file is not a session anyone needs to
// hand off, and asking for a checkpoint there turns every question into one.
func TestAStopAfterOnlyReadingAsksNothing(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	hookEvent(t, "UserPromptSubmit", `{"session_id":"s-read","prompt":"what does cart.go do"}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-read","tool_name":"Read","tool_input":{"file_path":"cart.go"}}`)

	if out := stopHook(t, `{"session_id":"s-read"}`); strings.TrimSpace(out) != "" {
		t.Errorf("a read-only session was asked to checkpoint:\n%s", out)
	}
}

// Work the agent already checkpointed is handed off; only work after the last
// checkpoint is at risk.
func TestAStopAfterTheAgentCheckpointedAsksNothing(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	hookEvent(t, "PostToolUse", `{"session_id":"s-done","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-done","tool_name":"mcp__plugin_logos_logos__checkpoint","tool_input":{"project":"shop"}}`)

	if out := stopHook(t, `{"session_id":"s-done"}`); strings.TrimSpace(out) != "" {
		t.Errorf("a session that checkpointed its work was asked again:\n%s", out)
	}
}

// Another session's uncheckpointed edits in the same project are not this
// session's to hand off.
func TestAStopDoesNotCountAnotherSessionsWork(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	hookEvent(t, "PostToolUse", `{"session_id":"s-other","tool_name":"Write","tool_input":{"file_path":"cart.go"}}`)
	hookEvent(t, "UserPromptSubmit", `{"session_id":"s-mine","prompt":"hello"}`)

	if out := stopHook(t, `{"session_id":"s-mine"}`); strings.TrimSpace(out) != "" {
		t.Errorf("a session was asked to checkpoint another session's edits:\n%s", out)
	}
}

// Claude Code reads a stop hook's decision from stdout, and record.sh sent
// stdout to /dev/null, so the request would never have reached it.
func TestTheStopHookPassesTheCheckpointRequestToClaudeCode(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/record.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo shop ;;
activity) case "$*" in *--ask-checkpoint*) echo '{"decision":"block","reason":"checkpoint"}' ;; esac ;;
esac`)

	cmd := exec.Command("/bin/bash", hook, "Stop")
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir()}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	if !strings.Contains(string(out), `"decision":"block"`) {
		t.Errorf("the Stop hook did not pass the checkpoint request through:\n%s", out)
	}

	cmd = exec.Command("/bin/bash", hook, "PostToolUse")
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir()}
	if out, _ := cmd.Output(); len(out) != 0 {
		t.Errorf("a tool-call hook printed on stdout:\n%s", out)
	}
}

// session-end.sh said "Next session resumes from here" after a session that
// saved nothing but a "session ended" note, and the next session restored
// nothing.
func TestSessionEndDoesNotClaimTheNextSessionResumesWhenNothingWasSaved(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-end.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo shop ;;
note) echo "noted" ;;
esac`)

	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "resumes from here") {
		t.Errorf("session-end claimed a resume point with no checkpoint:\n%s", out)
	}
}
