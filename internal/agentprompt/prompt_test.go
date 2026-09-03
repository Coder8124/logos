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
	repo, err := os.ReadFile(filepath.Join("..", "..", "systemmd", "BRAINPROMPT.md"))
	if err != nil {
		t.Skipf("no repository copy to compare against: %v", err)
	}
	if strings.TrimSpace(string(repo)) != strings.TrimSpace(Text()) {
		t.Error("systemmd/BRAINPROMPT.md and the embedded copy have drifted — regenerate with `make prompt` or copy it across")
	}
}
