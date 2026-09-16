package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeWithPluginCommands puts a `claude` on PATH that answers the plugin
// commands the real 2.1 CLI has, and records every call. `mcp list` reports the
// entry an earlier setup wrote, so removing it can be observed.
func claudeWithPluginCommands(t *testing.T, home string) string {
	t.Helper()
	bin := filepath.Join(home, "bin")
	calls := filepath.Join(home, "claude-calls")
	fakeProgram(t, bin, "claude", `echo "$*" >> `+calls+`
case "$1 $2" in
"plugin --help") echo "plugin marketplace add|install|update" ;;
"mcp list") if [ -e `+home+`/entry ]; then echo "logos: /bin/logos mcp serve - ✓ Connected"; fi ;;
"mcp remove") rm -f `+home+`/entry ;;
"plugin marketplace") ;;
"plugin install") ;;
"plugin update") ;;
esac`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	return string(raw)
}

// A brew or npx user got `claude mcp add` and a paragraph of /plugin steps to
// follow by hand, so the part of Logos that works without the model's
// cooperation — the session restore, the recording hooks, the skills — was
// only ever installed by the people who read the text. Claude Code's CLI
// installs it without a human in /plugin.
func TestSetupInstallsTheClaudeCodePluginRatherThanRegisteringTheServer(t *testing.T) {
	dir := setupInFakeHome(t)
	home := os.Getenv("HOME")
	calls := claudeWithPluginCommands(t, home)
	// The entry an earlier `logos setup` wrote: the plugin carries the same
	// server, so it must not be left loading logos a second time.
	if err := os.WriteFile(filepath.Join(home, "entry"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeHosts(t, "Claude Code")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	ran := readCalls(t, calls)
	if !strings.Contains(ran, "plugin marketplace add Coder8124/logos") || !strings.Contains(ran, "plugin install logos@logos") {
		t.Errorf("setup did not install the plugin:\n%s", ran)
	}
	if !strings.Contains(ran, "mcp remove --scope user logos") {
		t.Errorf("the entry an earlier setup wrote was left beside the plugin:\n%s", ran)
	}
	if !strings.Contains(out, "installed the Logos plugin") {
		t.Errorf("setup did not report the plugin install:\n%s", out)
	}
}

// A plugin older than this binary keeps running its old hooks against the new
// server; setup used to print the two commands and leave them to the user.
func TestSetupUpdatesAPluginOlderThanThisLogos(t *testing.T) {
	dir := setupInFakeHome(t)
	home := os.Getenv("HOME")
	calls := claudeWithPluginCommands(t, home)
	writeClaudeJSON(t, home, "plugins/installed_plugins.json", `{"version":2,"plugins":{"logos@logos":[{"scope":"user","version":"0.1.2"}]}}`)
	withVersion(t, "v0.4.3")
	fakeHosts(t, "Claude Code")

	captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	ran := readCalls(t, calls)
	if !strings.Contains(ran, "plugin marketplace update logos") || !strings.Contains(ran, "plugin update logos@logos") {
		t.Errorf("setup did not update the stale plugin:\n%s", ran)
	}
}

// --dry-run writes nothing, including through another program's CLI.
func TestADryRunPrintsThePluginCommandsAndRunsNone(t *testing.T) {
	dir := setupInFakeHome(t)
	home := os.Getenv("HOME")
	calls := claudeWithPluginCommands(t, home)
	fakeHosts(t, "Claude Code")

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--dry-run"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if !strings.Contains(out, "claude plugin install logos@logos --scope user") {
		t.Errorf("the dry run did not show the command it would run:\n%s", out)
	}
	if ran := readCalls(t, calls); strings.Contains(ran, "plugin install") {
		t.Errorf("the dry run installed the plugin:\n%s", ran)
	}
}

// writeClaudeJSON writes one of Claude Code's files under a fake HOME.
func writeClaudeJSON(t *testing.T, home, rel, content string) {
	t.Helper()
	path := filepath.Join(home, ".claude", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
