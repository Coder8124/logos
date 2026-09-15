package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loggingStub writes an executable at path that records its arguments in
// $HOME/calls, so a test can see which copy of a CLI setup ran.
func loggingStub(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$0 $*\" >> \"$HOME/calls\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// useCodexApp points the Codex.app lookup at path for one test, with bundled
// lookups on even under testenv.
func useCodexApp(t *testing.T, path string) {
	old := codexApp
	codexApp = path
	t.Cleanup(func() { codexApp = old })
	t.Setenv(noBundledCLIsEnv, "")
}

// Someone who uses Codex only as the desktop app has no `codex` on PATH, but
// the app ships the same CLI inside its bundle, and setup called Codex "not
// installed" and wired nothing.
func TestCodexInstalledAsTheDesktopAppIsWiredThroughItsBundledCLI(t *testing.T) {
	home := fakeHome(t, ".codex")
	t.Setenv("PATH", "/usr/bin:/bin")
	app := filepath.Join(t.TempDir(), "Codex.app", "Contents", "Resources", "codex")
	loggingStub(t, app)
	useCodexApp(t, app)

	r := Install(server(), []Host{codex()})

	if r[0].Outcome == Skipped {
		t.Fatalf("Codex with only the desktop app installed was reported %q", r[0].Outcome)
	}
	calls, _ := os.ReadFile(filepath.Join(home, "calls"))
	if !strings.Contains(string(calls), app+" mcp add "+Name) {
		t.Errorf("setup did not register through the app's codex:\n%s", calls)
	}
}

// The Claude Code extension for VS Code bundles its own `claude`, so an
// extension-only user has one without it being on PATH.
func TestClaudeCodeInstalledAsTheVSCodeExtensionIsWiredThroughItsBundledCLI(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	useCodexApp(t, filepath.Join(home, "no-codex-app"))
	bundled := filepath.Join(home, ".vscode", "extensions", "anthropic.claude-code-2.1.270-darwin-arm64", "resources", "native-binary", "claude")
	loggingStub(t, bundled)

	r := Install(server(), []Host{claudeCode()})

	if r[0].Outcome == Skipped {
		t.Fatalf("Claude Code with only the VS Code extension installed was reported %q", r[0].Outcome)
	}
	calls, _ := os.ReadFile(filepath.Join(home, "calls"))
	if !strings.Contains(string(calls), bundled+" mcp add --scope user "+Name) {
		t.Errorf("setup did not register through the extension's claude:\n%s", calls)
	}
}

// A CLI on PATH is the one the user runs, so it wins over a bundled copy that
// may be a different version.
func TestACLIOnPathIsPreferredOverABundledOne(t *testing.T) {
	home := fakeHome(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	useCodexApp(t, filepath.Join(home, "no-codex-app"))
	loggingStub(t, filepath.Join(bin, "claude"))
	loggingStub(t, filepath.Join(home, ".vscode", "extensions", "anthropic.claude-code-2.1.270-darwin-arm64", "resources", "native-binary", "claude"))

	if got := claudeCLI(); got != filepath.Join(bin, "claude") {
		t.Errorf("claudeCLI() = %q, want the one on PATH", got)
	}
}

// An extension folder with no executable claude in it (an older extension
// that drove a separately installed CLI) is not a Claude Code install.
func TestAnExtensionFolderWithoutABundledClaudeIsNotDetected(t *testing.T) {
	home := fakeHome(t, filepath.Join(".vscode", "extensions", "anthropic.claude-code-1.0.0"))
	t.Setenv("PATH", "/usr/bin:/bin")
	useCodexApp(t, filepath.Join(home, "no-codex-app"))

	if claudeCode().Detect() {
		t.Error("an extension folder with no claude binary was taken for Claude Code")
	}
}
