package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude puts a `claude` on PATH that behaves like the real one where it
// matters: `mcp add` refuses a name that already exists, with the message
// Claude Code prints, and `mcp remove` deletes it. Nothing here may run the
// real CLI — it would rewrite the developer's own ~/.claude.json.
func fakeClaude(t *testing.T) (config string) {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	config = filepath.Join(home, ".claude.json")
	stub := `#!/bin/sh
f="$HOME/.claude.json"
case "$2" in
add)
  if [ -s "$f" ]; then echo "MCP server brain already exists in user config"; exit 1; fi
  echo "$*" > "$f" ;;
remove)
  : > "$f" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return config
}

// Claude Code refuses to add a name that exists, and that refusal was read as
// success. Re-running setup with a different vault printed "✓ already
// connected" while Claude Code went on launching brain against the old one —
// the terminal on one vault, Claude Code on another, reported as healthy.
func TestRerunningSetupRepointsClaudeCodeAtTheNewVault(t *testing.T) {
	config := fakeClaude(t)
	first := server()
	first.Env = map[string]string{"BRAIN_VAULT": "/vaults/A"}
	second := server()
	second.Env = map[string]string{"BRAIN_VAULT": "/vaults/B"}

	Install(first, []Host{claudeCode()})
	results := Install(second, []Host{claudeCode()})

	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "BRAIN_VAULT=/vaults/B") {
		t.Errorf("Claude Code still has the old registration:\n%s", raw)
	}
	if results[0].Outcome != Updated {
		t.Errorf("outcome = %q (%v), want %q", results[0].Outcome, results[0].Err, Updated)
	}
}

// Replacing the entry must not turn every re-run into a change: the same
// server registered again is still "already connected".
func TestRerunningSetupWithNothingChangedStaysAlreadyConnected(t *testing.T) {
	fakeClaude(t)
	Install(server(), []Host{claudeCode()})
	results := Install(server(), []Host{claudeCode()})
	if results[0].Outcome != Unchanged {
		t.Errorf("outcome = %q (%v), want %q", results[0].Outcome, results[0].Err, Unchanged)
	}
}
