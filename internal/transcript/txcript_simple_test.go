package transcript_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
)

// txcript's own L1 example, verbatim from docs/formats/simple.md. Any session
// with a tool call in it is shaped like this, so it is what every real export
// looks like.
const simpleL1 = `{
  "messages": [
    { "role": "user", "content": "run the tests" },
    { "role": "assistant", "content": [
        { "type": "thinking", "text": "cargo test covers it." },
        { "type": "tool_use", "name": "Bash", "input": { "command": "cargo test" } }
    ] },
    { "role": "user", "content": [
        { "type": "tool_result", "content": "42 passed" }
    ] },
    { "role": "assistant", "content": "All green." }
  ]
}`

// #172: content was typed as a string, so the first block array failed the
// whole document with "cannot unmarshal array into Go struct field".
func TestASimpleDocumentWithToolCallsIsReadWithTheCallPairedToItsResult(t *testing.T) {
	s, err := transcript.ReadInterchange(strings.NewReader(simpleL1))
	if err != nil {
		t.Fatalf("ReadInterchange: %v", err)
	}
	if got := strings.Join(roles(s), ","); got != "user,tool,assistant" {
		t.Fatalf("roles = %s, want user,tool,assistant (thinking left out)", got)
	}
	tool := s.Turns[1]
	if tool.Tool != "Bash" || tool.Input != "cargo test" || tool.Text != "42 passed" || tool.Status != "ok" {
		t.Errorf("an id-less result did not pair with the call before it: %+v", tool)
	}
	for _, turn := range s.Turns {
		if strings.Contains(turn.Text, "cargo test covers it") {
			t.Errorf("reasoning was read as something that happened: %+v", turn)
		}
	}
}

// Explicit ids pair out of order, and an error result says so.
func TestSimpleToolResultsPairByIdWhenTheyGiveOne(t *testing.T) {
	const doc = `{"messages": [
	  {"role": "assistant", "content": [
	    {"type": "tool_use", "id": "a", "name": "Read", "input": {"file_path": "go.mod"}},
	    {"type": "tool_use", "id": "b", "name": "Bash", "input": {"command": "make"}}
	  ]},
	  {"role": "user", "content": [
	    {"type": "tool_result", "tool_use_id": "b", "is_error": true, "content": "make: *** no rule"},
	    {"type": "tool_result", "tool_use_id": "a", "content": [{"type": "text", "text": "module x"}]}
	  ]}
	]}`
	s, err := transcript.ReadInterchange(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadInterchange: %v", err)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("want two tool turns, got %+v", s.Turns)
	}
	if s.Turns[0].Input != "make" || s.Turns[0].Status != "error" {
		t.Errorf("result b did not pair with call b: %+v", s.Turns[0])
	}
	if s.Turns[1].Input != "go.mod" || s.Turns[1].Text != "module x" {
		t.Errorf("result a did not pair with call a: %+v", s.Turns[1])
	}
}

// #172: the reader ran `txcript export --format json <path>`. txcript has no
// --format flag and export takes a session id, so every call failed. A stand-in
// txcript on PATH records what it was asked and answers as the real one does.
func TestATxcriptSessionIsExportedByIdFromItsHarness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in txcript is a shell script")
	}
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nprintf '%s' '" + simpleL1 + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "txcript"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	s, err := transcript.ReadFile("opencode", "ses_4f2a")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	raw, _ := os.ReadFile(argsFile)
	if got := strings.Fields(string(raw)); strings.Join(got, " ") != "export ses_4f2a --from opencode" {
		t.Errorf("txcript was called as %q, want export <id> --from opencode", got)
	}
	if s.Harness != "opencode" || len(s.Turns) != 3 {
		t.Errorf("session = %s with %d turns", s.Harness, len(s.Turns))
	}
}

// JSON null unmarshals into a slice without error, so a message whose content
// was null took the block path, came out as no turns at all, and its text and
// tool fields — the only place the message said anything — were dropped.
func TestAMessageWithNullContentKeepsItsTextAndTool(t *testing.T) {
	doc := `{"messages": [
	  {"role": "assistant", "content": null, "text": "ran the tests"},
	  {"role": "tool", "content": null, "tool": "Bash", "text": "42 passed"}
	]}`
	s, err := transcript.ReadInterchange(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadInterchange: %v", err)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("want two turns, got %+v", s.Turns)
	}
	if s.Turns[0].Text != "ran the tests" || s.Turns[1].Tool != "Bash" || s.Turns[1].Text != "42 passed" {
		t.Errorf("null content lost the message's own fields: %+v", s.Turns)
	}
}
