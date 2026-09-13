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
