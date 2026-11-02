package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// Setup wires Claude Code under CLAUDE_CONFIG_DIR; harvest read
// ~/.claude/projects regardless, and found nothing while the host wrote its
// transcripts in the moved directory (#376).
func TestClaudeCodeTranscriptsAreReadUnderAMovedConfigDir(t *testing.T) {
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LogosClaudeProjectsEnv, "")
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("HOME", t.TempDir())
	if got, want := (claudeCodeReader{}).root(), filepath.Join(config, "projects"); got != want {
		t.Errorf("root() = %q, want %q", got, want)
	}
}

func TestCodexTranscriptsAreReadUnderAMovedCodexHome(t *testing.T) {
	codex := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codex, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LogosCodexSessionsEnv, "")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("HOME", t.TempDir())
	if got, want := (codexReader{}).root(), filepath.Join(codex, "sessions"); got != want {
		t.Errorf("root() = %q, want %q", got, want)
	}
}
