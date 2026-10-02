package advice

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// The two front ends drifted when each built its own receipt. Here the same
// checkpoint gets the same nudges from both, and they differ only in how a
// field is spelled.
func TestBothFrontEndsGiveTheSameNudgesInTheirOwnSpelling(t *testing.T) {
	c := session.Checkpoint{
		Project:   "kestrel",
		State:     "fixed and merged",
		Next:      "if asked: populate Appendix A, otherwise quote the extruded option",
		Decisions: []string{"use sqlite", "use extruded frames"},
		Failed:    []string{"brain binary not on PATH"},
	}
	cli := Checkpoint(c, nil, 1, CLI)
	mcp := Checkpoint(c, nil, 1, MCP)
	if len(cli) != 4 || len(mcp) != len(cli) {
		t.Fatalf("got %d CLI and %d MCP sentences, want 4 each:\n%s\n---\n%s",
			len(cli), len(mcp), strings.Join(cli, "\n"), strings.Join(mcp, "\n"))
	}
	for i, want := range []string{"placeholder", "more than one step", "without a reason", "layer: environment"} {
		if !strings.Contains(cli[i], want) || !strings.Contains(mcp[i], want) {
			t.Errorf("sentence %d does not say %q in both:\n%s\n%s", i, want, cli[i], mcp[i])
		}
	}
	for _, want := range []string{"--next", "--question", "2 --decided entries", "1 --failed entry"} {
		if !strings.Contains(strings.Join(cli, "\n"), want) {
			t.Errorf("the CLI receipt does not name %s", want)
		}
	}
	for _, want := range []string{"`next`", "`questions`", "2 decisions", "1 ruled-out approach"} {
		if !strings.Contains(strings.Join(mcp, "\n"), want) {
			t.Errorf("the MCP receipt does not name %s", want)
		}
	}
}

func TestAPlainCheckpointGetsNoAdvice(t *testing.T) {
	c := session.Checkpoint{
		Project:   "kestrel",
		State:     "fixed and merged",
		Verified:  []string{"go test ./... passes"},
		Next:      "quote the extruded option",
		Decisions: []string{"use sqlite, because the vault stays on this machine"},
		Failed:    []string{"retrying hid the race"},
	}
	if got := Checkpoint(c, nil, 0, MCP); len(got) != 0 {
		t.Errorf("a checkpoint with nothing to doubt got advice:\n%s", strings.Join(got, "\n"))
	}
}
