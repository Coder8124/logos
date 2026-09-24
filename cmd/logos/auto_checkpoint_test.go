package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// endSession runs the note the SessionEnd hook runs, with its payload on stdin.
func endSession(t *testing.T, sessionID string) string {
	t.Helper()
	t.Setenv("LOGOS_NOTE_IF_UNCOMMITTED", "1")
	t.Setenv("LOGOS_AUTO_CHECKPOINT", "1")
	restore := setStdin(t, []byte(`{"session_id":"`+sessionID+`","reason":"prompt_input_exit"}`))
	defer restore()
	return captureStdout(t, func() {
		if err := runNote([]string{"shop", "claude-code session ended"}); err != nil {
			t.Fatalf("session end: %v", err)
		}
	})
}

// A session that edited and ran commands and closed without a checkpoint left
// the next session nothing, though the activity log had recorded what it did.
// What is written from that record is labelled as machine-written, and says
// nothing about what was verified or ruled out, because nobody said.
func TestASessionEndingWithUncheckpointedWorkLeavesALabelledAutoCheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	hookEvent(t, "UserPromptSubmit", `{"session_id":"s-auto","prompt":"fix the checkout crash when cart is empty"}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-auto","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-auto","tool_name":"Bash","tool_input":{"command":"go test ./cart"}}`)

	out := endSession(t, "s-auto")
	if !strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("the session end did not report the auto checkpoint, got %q", out)
	}
	c, err := session.Latest(vaultPath(), "shop")
	if err != nil || c == nil {
		t.Fatalf("no checkpoint after a session that did work: %v", err)
	}
	if !c.Auto {
		t.Error("the checkpoint is not marked auto")
	}
	if !strings.Contains(c.Task, "fix the checkout crash") {
		t.Errorf("task is not the session's first prompt: %q", c.Task)
	}
	if !strings.Contains(strings.Join(c.Files, " "), "cart.go") || !strings.Contains(strings.Join(c.Commands, " "), "go test ./cart") {
		t.Errorf("files %v or commands %v are missing what the session did", c.Files, c.Commands)
	}
	if len(c.Verified) != 0 || len(c.Failed) != 0 {
		t.Errorf("an auto checkpoint claimed verified %v or failed %v", c.Verified, c.Failed)
	}

	resumed := captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(resumed, "not written by an agent, unverified") {
		t.Errorf("resume does not show the auto label:\n%s", resumed)
	}
}

// Claude Code ends a resumed session again under the same id; the work already
// recorded must not come back as a second, identical auto checkpoint.
func TestASessionEndingTwiceWritesOneAutoCheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	hookEvent(t, "PostToolUse", `{"session_id":"s-twice","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	endSession(t, "s-twice")

	if out := endSession(t, "s-twice"); strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("a second end with nothing new wrote another auto checkpoint: %q", out)
	}
}

// The agent's own checkpoint is the real handoff; an auto one written after it
// would become the latest and bury it.
func TestAnAutoCheckpointIsNotWrittenAfterTheAgentCheckpointed(t *testing.T) {
	standIn(t, "elsewhere")
	hookEvent(t, "PostToolUse", `{"session_id":"s-own","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-own","tool_name":"mcp__plugin_logos_logos__checkpoint","tool_input":{"project":"shop"}}`)

	endSession(t, "s-own")
	if c, _ := session.Latest(vaultPath(), "shop"); c == nil || c.Auto {
		t.Errorf("the agent's checkpoint was outranked by an auto one: %+v", c)
	}
}

// A session that only read is not worth a handoff, and an auto checkpoint
// there would replace a real one from an earlier session as the latest.
func TestASessionThatOnlyReadLeavesNoAutoCheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	hookEvent(t, "UserPromptSubmit", `{"session_id":"s-look","prompt":"what does cart.go do"}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-look","tool_name":"Read","tool_input":{"file_path":"cart.go"}}`)

	endSession(t, "s-look")
	if c, _ := session.Latest(vaultPath(), "shop"); c != nil {
		t.Errorf("a read-only session left a checkpoint: %+v", c)
	}
}

// The hook has to hand logos its payload and ask for the auto checkpoint, and
// then say what was saved.
func TestTheSessionEndHookAsksForAnAutoCheckpointAndSaysOne(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-end.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo shop ;;
note) if [ "$LOGOS_AUTO_CHECKPOINT" = 1 ] && grep -q s-hook; then echo "auto-checkpointed — sessions/shop/x"; else echo noted; fi ;;
esac`)

	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir()}
	cmd.Stdin = strings.NewReader(`{"session_id":"s-hook"}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "automatic checkpoint") {
		t.Errorf("the hook did not pass its payload through or did not report the auto checkpoint:\n%s", out)
	}
}

// The hook's record is dated when the session closed, which can be long after
// its transcript's last line; the sweep matched it by time and recorded the
// session a second time when the close ran late. The record names the session
// in full — the id the transcript's own file is called — so it is matched
// exactly.
func TestTheHookRecordNamesTheSessionItCameFrom(t *testing.T) {
	standIn(t, "elsewhere")
	id := "6f1c2a9e-3b7d-4e2a-9c11-5d0e8f7a4b21"
	hookEvent(t, "PostToolUse", `{"session_id":"`+id+`","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	endSession(t, id)

	c, err := session.Latest(vaultPath(), "shop")
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if !strings.Contains(c.State, "("+id+")") {
		t.Errorf("the hook record does not name its session in full: %q", c.State)
	}
}
