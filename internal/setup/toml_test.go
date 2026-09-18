package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// #90: Codex was the one host whose registrations nothing could read back, so
// a Codex wired to a binary that has since been deleted, or to a path inside
// npm's npx cache, passed every check doctor makes — all three of them go
// through Host.List. Reading them off config.toml is what puts Codex under
// the same checks as everyone else.
func TestCodexListsWhatItsConfigFileHasRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `model = "gpt-5"

[mcp_servers.logos]
command = "/opt/homebrew/bin/logos"
args = ["mcp", "serve"]

[mcp_servers.logos.env]
LOGOS_VAULT = "/Users/bob/brain"

[mcp_servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]
`
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	regs, err := codex().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 {
		t.Fatalf("Codex listed %d servers, not the 2 in its config: %+v", len(regs), regs)
	}
	if regs[0].Name != "logos" || regs[0].Command != "/opt/homebrew/bin/logos mcp serve" || regs[0].Vault != "/Users/bob/brain" {
		t.Errorf("the logos entry came back as %+v", regs[0])
	}
	if regs[1].Name != "filesystem" || regs[1].Command != "npx -y @modelcontextprotocol/server-filesystem" {
		t.Errorf("another server's entry came back as %+v", regs[1])
	}
}

// A Codex that has never been configured is not an error — nothing registered
// is a valid answer, and the same one readServerBlock gives for a missing JSON
// config. A caller must not be told the host is broken because it is new.
func TestACodexWithNoConfigFileListsNothingAndDoesNotFail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	regs, err := codex().List()
	if err != nil || len(regs) != 0 {
		t.Errorf("an unconfigured Codex reported %d servers, err %v", len(regs), err)
	}
}
