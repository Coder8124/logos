package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.4 setup registered the server as "brain". Adding "logos" beside it left
// every host loading the server twice, the old copy on whatever binary and
// vault 0.4 pinned, and setup's answer was to tell the user to remove it.
func TestSetupReplacesTheOldBrainEntryInAJSONHost(t *testing.T) {
	fakeHome(t, ".cursor")
	h := cursor()
	old := `{"mcpServers": {"brain": {"command": "/Users/x/.local/bin/brain", "args": ["mcp", "serve"]}, "theirs": {"command": "theirs"}}}`
	if err := os.WriteFile(h.Config(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{h})

	servers := readServers(t, h.Config())
	if _, ok := servers[OldName]; ok {
		t.Errorf("the brain entry is still there:\n%+v", servers)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("the user's other server was lost:\n%+v", servers)
	}
	if servers[Name].Command != "/usr/local/bin/logos" {
		t.Errorf("logos entry is %+v", servers[Name])
	}
	if !r[0].Replaced || r[0].ReplaceErr != nil {
		t.Errorf("Replaced = %v (%v), want true so setup can say so", r[0].Replaced, r[0].ReplaceErr)
	}
}

// "brain" is a common enough word that someone else's server may use it.
// Only an entry that runs `mcp serve` is ours to remove.
func TestABrainEntryThatIsNotLogosIsKept(t *testing.T) {
	fakeHome(t, ".cursor")
	h := cursor()
	old := `{"mcpServers": {"brain": {"command": "uvx", "args": ["some-other-brain"]}}}`
	if err := os.WriteFile(h.Config(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{h})

	if _, ok := readServers(t, h.Config())[OldName]; !ok {
		t.Error("someone else's server named brain was removed")
	}
	if r[0].Replaced {
		t.Error("Replaced is set for an entry that was not logos")
	}
}

// stubCLI puts a fake bin on PATH that logs every call and runs body.
func stubCLI(t *testing.T, bin, body string) (home, calls string) {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	calls = filepath.Join(home, "calls")
	stub := "#!/bin/sh\necho \"$*\" >> \"$HOME/calls\"\n" + body
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, calls
}

func TestSetupRemovesTheOldBrainEntryFromClaudeCode(t *testing.T) {
	home, calls := stubCLI(t, "claude", `
case "$2" in
list) [ -f "$HOME/old" ] && echo "brain: /Users/x/.local/bin/brain mcp serve - ✓ Connected" ;;
get) [ "$3" = brain ] && [ -f "$HOME/old" ] || { echo "No MCP server found with name: $3" >&2; exit 1; }
  printf 'brain:\n  Scope: User config\n  Status: ✓ Connected\n  Type: stdio\n  Command: /Users/x/.local/bin/brain\n  Args: mcp serve\n  Environment:\n' ;;
remove) [ "$5" = brain ] && rm -f "$HOME/old" ;;
esac
exit 0
`)
	if err := os.WriteFile(filepath.Join(home, "old"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{claudeCode()})

	log, _ := os.ReadFile(calls)
	if !strings.Contains(string(log), "mcp remove --scope user brain") {
		t.Errorf("the brain entry was not removed; calls:\n%s", log)
	}
	if !r[0].Replaced || r[0].ReplaceErr != nil {
		t.Errorf("Replaced = %v (%v), want true", r[0].Replaced, r[0].ReplaceErr)
	}
	// `mcp list` starts every server Claude Code has to report its health,
	// and plugin.go stopped using it because one failed start of the
	// plugin's server turns Logos off for fifteen minutes. Looking for one
	// old entry by name needs only that name.
	if strings.Contains(string(log), "mcp list") {
		t.Errorf("looking for the brain entry health-checked every server; calls:\n%s", log)
	}
}

// Someone else's server called brain, seen through `mcp get`, is still kept.
func TestABrainEntryInClaudeCodeThatIsNotLogosIsKept(t *testing.T) {
	_, calls := stubCLI(t, "claude", `
case "$2" in
get) printf 'brain:\n  Scope: User config\n  Type: stdio\n  Command: uvx\n  Args: some-other-brain\n' ;;
esac
exit 0
`)

	r := Install(server(), []Host{claudeCode()})

	log, _ := os.ReadFile(calls)
	if strings.Contains(string(log), "remove --scope user brain") || r[0].Replaced {
		t.Errorf("someone else's brain server was removed (Replaced %v); calls:\n%s", r[0].Replaced, log)
	}
}

func TestSetupRemovesTheOldBrainEntryFromCodex(t *testing.T) {
	home, calls := stubCLI(t, "codex", `
[ "$2" = remove ] && : > "$HOME/.codex/config.toml"
exit 0
`)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	toml := "[mcp_servers.brain]\ncommand = \"/Users/x/.local/bin/brain\"\nargs = [\"mcp\", \"serve\"]\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Install(server(), []Host{codex()})

	log, _ := os.ReadFile(calls)
	if !strings.Contains(string(log), "mcp remove brain") {
		t.Errorf("the brain entry was not removed; calls:\n%s", log)
	}
	if !r[0].Replaced || r[0].ReplaceErr != nil {
		t.Errorf("Replaced = %v (%v), want true", r[0].Replaced, r[0].ReplaceErr)
	}
}

// A Codex with no brain entry must not be asked to remove one: the removal
// fails, and that failure would be reported against a clean install.
func TestCodexWithoutAnOldEntryIsNotAskedToRemoveOne(t *testing.T) {
	_, calls := stubCLI(t, "codex", "exit 0\n")

	r := Install(server(), []Host{codex()})

	log, _ := os.ReadFile(calls)
	if strings.Contains(string(log), "remove") || r[0].Replaced || r[0].ReplaceErr != nil {
		t.Errorf("a clean Codex was asked to remove brain (Replaced %v, %v); calls:\n%s", r[0].Replaced, r[0].ReplaceErr, log)
	}
}

// `mcp get` failing for any reason read as "no brain entry here", so a broken
// claude CLI left the old server in place and setup reported a clean run. Only
// Claude Code's own "no such server" answer means there is nothing to remove.
func TestAFailingClaudeMCPGetIsReportedNotTakenForNoEntry(t *testing.T) {
	stubCLI(t, "claude", `
[ "$2" = get ] && { echo "Error: could not parse ~/.claude.json" >&2; exit 2; }
exit 0
`)

	r := Install(server(), []Host{claudeCode()})

	if r[0].ReplaceErr == nil || !strings.Contains(r[0].ReplaceErr.Error(), "could not parse") {
		t.Errorf("ReplaceErr = %v, want the CLI's failure", r[0].ReplaceErr)
	}
}
