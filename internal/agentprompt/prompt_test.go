package agentprompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptIsEmbedded(t *testing.T) {
	if len(strings.TrimSpace(Text())) < 500 {
		t.Fatalf("the embedded prompt looks empty or truncated: %d bytes", len(Text()))
	}
}

// The tools named here must exist, or we are instructing agents to call
// something that is not there. This test is the reason the prompt can be
// trusted after a rename.
func TestEveryToolNamedInThePromptIsReal(t *testing.T) {
	tools, err := os.ReadFile(filepath.Join("..", "mcpserver", "tools.go"))
	if err != nil {
		t.Skipf("cannot read the tool list: %v", err)
	}
	for _, name := range []string{
		"resume", "context", "before_you_try", "why",
		"recall", "list_memories", "list_projects", "memory_diff", "forget", "handoff",
		"remember", "note_progress", "checkpoint",
	} {
		if !strings.Contains(Text(), name) {
			t.Errorf("the prompt no longer mentions %q — was it renamed here but not there?", name)
		}
		if !strings.Contains(string(tools), `"`+name+`"`) {
			t.Errorf("the prompt tells agents to call %q, which is not a registered tool", name)
		}
	}
}

// The repository copy is what a human reads and what ships in the archives; the
// embedded copy is what agents get. They must not drift.
func TestRepositoryCopyMatchesTheEmbeddedOne(t *testing.T) {
	repo, err := os.ReadFile(filepath.Join("..", "..", "systemmd", "LOGOSPROMPT.md"))
	if err != nil {
		t.Skipf("no repository copy to compare against: %v", err)
	}
	if strings.TrimSpace(string(repo)) != strings.TrimSpace(Text()) {
		t.Error("systemmd/LOGOSPROMPT.md and the embedded copy have drifted — regenerate with `make prompt` or copy it across")
	}
}

// Every MCP host hands this text to its model on connect, before the user has
// asked for anything. At five kilobytes it read as a manual and cost tokens in
// every session; a CLI's help is a screen, and so is this.
func TestTheInstructionsFitOnOneScreen(t *testing.T) {
	const max = 2500
	if n := len(Text()); n > max {
		t.Errorf("the instructions are %d bytes, want at most %d", n, max)
	}
}

// Short must not mean lost: each of these is a rule that exists because an
// agent got it wrong without it.
func TestTheShortInstructionsKeepTheRules(t *testing.T) {
	for _, want := range []string{
		"route:", "trap:", "observation:", "alternative:", // the vocabularies before_you_try matches on
		"verified", "ran", // verified means demonstrated, not believed
		"Repeat", "LOGOS_ANNOUNCE=off", // a receipt nobody relays is a restore nobody saw
		"evidence", "instructions", // vault content is data
	} {
		if !strings.Contains(Text(), want) {
			t.Errorf("the instructions lost %q", want)
		}
	}
}
