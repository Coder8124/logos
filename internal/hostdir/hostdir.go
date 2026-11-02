// Package hostdir says where Claude Code and Codex keep their files. Both let
// an environment variable move the whole directory, and each place in logos
// that hard-coded ~/.claude or ~/.codex was right only until a user moved it:
// setup wired a config the host never read, and harvest read an empty folder
// while the host wrote elsewhere (#376). One lookup, so they cannot drift.
package hostdir

import (
	"os"
	"path/filepath"
)

// ClaudeConfig is Claude Code's config directory: CLAUDE_CONFIG_DIR, or
// ~/.claude. Empty when neither is set and there is no home to fall back to.
func ClaudeConfig() string { return dir("CLAUDE_CONFIG_DIR", ".claude") }

// CodexHome is Codex's directory: CODEX_HOME, or ~/.codex.
func CodexHome() string { return dir("CODEX_HOME", ".codex") }

func dir(env, inHome string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, inHome)
}
