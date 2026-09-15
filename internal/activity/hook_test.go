package activity

import (
	"fmt"
	"strings"
	"testing"
)

func TestPostToolUseNamesTheThingThatHappened(t *testing.T) {
	raw := []byte(`{"session_id":"a2b97274-634b-4129-9c0f-11","cwd":"/Users/x/kestrel",
	 "hook_event_name":"PostToolUse","tool_name":"Edit",
	 "tool_input":{"file_path":"src/acp-driver.rs","old_string":"a","new_string":"b"}}`)
	e, err := FromHook("PostToolUse", raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != KindTool || e.Tool != "Edit" {
		t.Fatalf("kind/tool wrong: %+v", e)
	}
	if e.Summary != "Edit src/acp-driver.rs" {
		t.Errorf("summary should name the file, got %q", e.Summary)
	}
	if e.Project != "kestrel" {
		t.Errorf("project should fall back to the cwd's basename, got %q", e.Project)
	}
	if e.Session != "a2b97274" {
		t.Errorf("session should be shortened for scanning, got %q", e.Session)
	}
}

func TestBashSummaryCarriesTheCommand(t *testing.T) {
	raw := []byte(`{"tool_name":"Bash","tool_input":{"command":"cargo test --workspace"}}`)
	e, _ := FromHook("PostToolUse", raw, "kestrel")
	if e.Summary != "Bash: cargo test --workspace" {
		t.Errorf("got %q", e.Summary)
	}
}

func TestPromptIsFlattenedToOneLine(t *testing.T) {
	raw := []byte(`{"prompt":"wire the codex adapter\ninto the chat panel"}`)
	e, _ := FromHook("UserPromptSubmit", raw, "kestrel")
	if e.Kind != KindPrompt {
		t.Fatalf("kind: %q", e.Kind)
	}
	if e.Summary != "wire the codex adapter into the chat panel" {
		t.Errorf("newlines must not survive into a JSONL summary: %q", e.Summary)
	}
}

// An explicit project always wins over the cwd guess: the hook knows
// CLAUDE_PROJECT_DIR and the payload's cwd may be a subdirectory.
func TestExplicitProjectBeatsTheCwdGuess(t *testing.T) {
	raw := []byte(`{"cwd":"/Users/x/kestrel/internal/deep"}`)
	e, _ := FromHook("PostToolUse", raw, "kestrel")
	if e.Project != "kestrel" {
		t.Errorf("got %q", e.Project)
	}
}

// The payload comes from a host we do not ship. Being strict here would mean a
// field rename upstream silently switches the whole record off.
func TestUnparseablePayloadStillRecords(t *testing.T) {
	e, err := FromHook("PostToolUse", []byte("not json at all"), "kestrel")
	if err != nil {
		t.Fatalf("a broken payload must still produce a row: %v", err)
	}
	if e.Kind != KindTool || e.Project != "kestrel" {
		t.Errorf("%+v", e)
	}
}

func TestUnknownEventStillRecordsUnderItsOwnName(t *testing.T) {
	e, err := FromHook("SomeFutureEvent", []byte(`{}`), "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "somefutureevent" {
		t.Errorf("got %q", e.Kind)
	}
}

func TestNoEventNameIsRefused(t *testing.T) {
	if _, err := FromHook("", []byte(`{}`), "kestrel"); err == nil {
		t.Error("an event with no name should be refused")
	}
}

func TestToolResponseIsNotStored(t *testing.T) {
	raw := []byte(`{"tool_name":"Read","tool_input":{"file_path":"a.go"},
	 "tool_response":"` + string(make([]byte, 0)) + `a very large file body"}`)
	e, _ := FromHook("PostToolUse", raw, "kestrel")
	if _, ok := e.Extra["tool_response"]; ok {
		t.Error("tool responses must not be kept — the log would grow without bound")
	}
}

// The input used to be kept whole: every Write's content, every Edit's old
// and new text, every shell command, unredacted, in a log nothing prunes and
// nothing mentions. The one-line summary is what a reader of the log uses, so
// that is all that is kept, with secrets masked the way ingest masks them.
func TestToolInputBodiesAreNotStoredAndTheSummaryIsRedacted(t *testing.T) {
	raw := []byte(`{"tool_name":"Write","tool_input":{"file_path":"config.env","content":"STRIPE=sk_live_abcdefghijklmnopqrstuvwxyz0123"}}`)
	e, _ := FromHook("PostToolUse", raw, "kestrel")
	if len(e.Extra) != 0 || e.Summary != "Write config.env" {
		t.Errorf("a Write kept more than its path: summary %q, extra %v", e.Summary, e.Extra)
	}

	raw = []byte(`{"tool_name":"Bash","tool_input":{"command":"curl -H 'Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz0123456789' https://api.github.com"}}`)
	e, _ = FromHook("PostToolUse", raw, "kestrel")
	if strings.Contains(e.Summary, "ghp_") || strings.Contains(fmt.Sprint(e.Extra), "ghp_") {
		t.Errorf("a token in a shell command was stored: summary %q, extra %v", e.Summary, e.Extra)
	}
	if !strings.HasPrefix(e.Summary, "Bash: curl") {
		t.Errorf("redaction should mask the token, not the command: %q", e.Summary)
	}
}

func TestLongSummaryIsCutOnARuneBoundary(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "é"
	}
	raw := []byte(`{"prompt":"` + long + `"}`)
	e, _ := FromHook("UserPromptSubmit", raw, "p")
	for _, r := range e.Summary {
		if r == '�' {
			t.Fatal("cut mid-rune")
		}
	}
}
