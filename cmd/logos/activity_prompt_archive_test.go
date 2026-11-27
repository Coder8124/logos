package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/session"
)

func hookPromptTranscript(t *testing.T, cwd string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "-Users-me-code-eco-game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "3f9a-prompt-session.jsonl")
	line := fmt.Sprintf(`{"type":"user","sessionId":"3f9a-prompt-session","cwd":%q,"message":{"role":"user","content":"hi"}}`+"\n", cwd)
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func hookPromptPayload(event, path, cwd string) []byte {
	return []byte(fmt.Sprintf(`{"hook_event_name":%q,"session_id":"3f9a-prompt-session","transcript_path":%q,"cwd":%q,"prompt":"hi"}`, event, path, cwd))
}

func TestOnlyAPromptArchivesTheTranscript(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	cwd := "/Users/me/code/eco-game"
	src := hookPromptTranscript(t, cwd)

	restore := setStdin(t, hookPromptPayload("PostToolUse", src, cwd))
	if err := recordActivity([]string{"--event", "PostToolUse", "--project", "eco-game"}); err != nil {
		t.Fatal(err)
	}
	restore()
	if _, err := os.Stat(filepath.Join(vault, "sessions")); err == nil {
		t.Fatal("a tool call archived the transcript; once per prompt is the bound on the cost")
	}

	restore = setStdin(t, hookPromptPayload("UserPromptSubmit", src, cwd))
	defer restore()
	if err := recordActivity([]string{"--event", "UserPromptSubmit", "--project", "some-other-name"}); err != nil {
		t.Fatal(err)
	}
	// Filed under the transcript's own project, not the hook's --project.
	want := filepath.Join(vault, "sessions", "eco-game", "_convos", "3f9a-prompt-session", "transcript.jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("a prompt left no copy at %s: %v", want, err)
	}
}

func TestAPromptCopyThatCannotBeRedactedLeavesAWarningNotACopy(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write to")
	}
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	cwd := "/Users/me/code/eco-game"
	src := hookPromptTranscript(t, cwd)
	dir, err := session.TranscriptDir(vault, "eco-game", "3f9a-prompt-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	restore := setStdin(t, hookPromptPayload("UserPromptSubmit", src, cwd))
	defer restore()
	if err := recordActivity([]string{"--event", "UserPromptSubmit", "--project", "eco-game"}); err != nil {
		t.Fatalf("a failed archive failed the hook: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a failed redaction left %d files behind", len(entries))
	}
	warns, err := activity.Read(vault, activity.Query{Kind: activity.KindWarn})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Summary, "not archived") {
		t.Fatalf("want one warning row naming the failed archive, got %+v", warns)
	}
}
