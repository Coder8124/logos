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

// TOML comments run to end of line, and a value or a table header may carry
// one. Stopping only at whole-line comments left the comment inside the value:
// the command became `"/usr/local/bin/logos" # moved here mcp serve`, which
// doctor then probed and reported a correctly wired Codex as broken, and a
// commented table header was not recognised as a server at all, so the pin
// vanished.
func TestACommentAfterAValueIsNotPartOfIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `model = "gpt-5"

[mcp_servers.logos]   # the one that matters
command = "/usr/local/bin/logos"   # moved here
args = ["mcp", "serve"]

[mcp_servers.logos.env]
LOGOS_VAULT = "/Users/bob/brain"   # the good one
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	regs, err := readCodexServers(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 1 {
		t.Fatalf("read %d servers, want the one Codex has: %+v", len(regs), regs)
	}
	if regs[0].Command != "/usr/local/bin/logos mcp serve" {
		t.Errorf("command = %q, want the command without the comment", regs[0].Command)
	}
	if regs[0].Vault != "/Users/bob/brain" {
		t.Errorf("vault = %q, want the pin without the comment", regs[0].Vault)
	}
}

// Remove reads config.toml to decide whether there is anything to remove, and
// compared the table header literally: `[mcp_servers.logos] # logos` made it
// answer "nothing was removed" about an entry plainly there. The same comment
// bug the value parser was fixed for, on the same file.
func TestACodexEntryIsFoundWhenItsTableHeaderCarriesAComment(t *testing.T) {
	const cfg = `[mcp_servers.logos]  # logos
command = "logos"
args = ["mcp", "serve"]
`
	if !codexHasEntry(cfg, "logos") {
		t.Errorf("the logos entry was not found behind a comment on its header")
	}
}

// The mirror of it: a commented-out args line is not the table's contents, and
// reading it as such claimed an entry existed in an empty table.
func TestACommentedOutArgsLineIsNotACodexEntry(t *testing.T) {
	const cfg = `[mcp_servers.logos]
# args = ["mcp", "serve"]
`
	if codexHasEntry(cfg, "logos") {
		t.Errorf("a commented-out args line was read as a registered entry")
	}
}

// Codex's own documentation writes env as an inline table. Read as no env at
// all, an entry pinned that way looked unpinned — to doctor, and to migrate,
// which removes an unpinned 0.4 brain entry as a leftover of the vault it moves.
func TestAnInlineEnvTableIsReadAsTheEntrysEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := `[mcp_servers.brain]
command = "/opt/brain"
args = ["mcp", "serve"]
env = { BRAIN_VAULT = "/Users/x/other, vault", "LOGOS_VAULT" = 'C:\v' }
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	regs, err := readCodexServers(path)
	if err != nil || len(regs) != 1 {
		t.Fatalf("read %+v, %v", regs, err)
	}
	env := regs[0].Server.Env
	if env["BRAIN_VAULT"] != "/Users/x/other, vault" || env["LOGOS_VAULT"] != `C:\v` || regs[0].Vault != `C:\v` {
		t.Errorf("the inline env came back as %v, vault %q", env, regs[0].Vault)
	}
}
