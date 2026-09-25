package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var rewired = Server{Bin: "/opt/homebrew/bin/logos", Args: []string{"mcp", "serve"}, Env: map[string]string{"LOGOS_VAULT": "/Users/bob/logos"}}

// #205: a re-run of setup, or migrate's re-pin, replaced the whole entry. A
// server the user had switched off came back on, and their auto-approvals
// went, with the .logos-backup the only copy left.
func TestReRegisteringAJSONHostKeepsTheUsersOwnKeysOnTheLogosEntry(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	body := `{"mcpServers":{"logos":{"command":"logos","args":["mcp","serve"],"env":{"LOGOS_VAULT":"/old"},"disabled":true,"autoApprove":["recall"],"timeout":30}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeServers(cfg, "mcpServers", serverEntry{Command: rewired.Bin, Args: rewired.Args, Env: rewired.Env}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	var got struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	e := got.MCPServers["logos"]
	if e["disabled"] != true || e["timeout"] != float64(30) || e["autoApprove"] == nil {
		t.Errorf("the user's keys did not survive: %s", raw)
	}
	if e["command"] != rewired.Bin || e["env"].(map[string]any)["LOGOS_VAULT"] != "/Users/bob/logos" {
		t.Errorf("logos's own keys were not rewritten: %s", raw)
	}
}

func TestReRegisteringATOMLHostKeepsTheUsersOwnKeysOnTheLogosTable(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	body := `[mcp_servers.logos]
command = "logos"
args = ["mcp", "serve"]
enabled = false
startup_timeout_sec = 30
enabled_tools = [
  "recall",
  "resume",
]

[mcp_servers.logos.env]
LOGOS_VAULT = "/old"
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeTOMLServer(cfg, rewired); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	out := string(raw)
	for _, want := range []string{"enabled = false", "startup_timeout_sec = 30", "enabled_tools = [\n  \"recall\",\n  \"resume\",\n]", `command = "/opt/homebrew/bin/logos"`, `LOGOS_VAULT = "/Users/bob/logos"`} {
		if !strings.Contains(out, want) {
			t.Errorf("config lacks %q:\n%s", want, out)
		}
	}
	// The kept keys belong to the server's table, not its env table below.
	if i, j := strings.Index(out, "enabled = false"), strings.Index(out, "[mcp_servers.logos.env]"); i > j {
		t.Errorf("kept key landed in the env table:\n%s", out)
	}
}

// Codex went through `codex mcp add`, which replaces the table outright, so
// its entry is edited in its file like Grok Build's.
func TestReRegisteringCodexKeepsTheUsersOwnKeysOnTheLogosTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".codex", "config.toml")
	body := "[mcp_servers.logos]\ncommand = \"logos\"\nargs = [\"mcp\", \"serve\"]\nenabled = false\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codex().Register(rewired); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	if !strings.Contains(string(raw), "enabled = false") || !strings.Contains(string(raw), rewired.Bin) {
		t.Errorf("codex config after re-registering:\n%s", raw)
	}
}

func TestReRegisteringWithoutAPinTakesTheOldPinOffTheEntry(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	body := `{"mcpServers":{"logos":{"command":"logos","env":{"LOGOS_VAULT":"/old"},"disabled":true}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeServers(cfg, "mcpServers", serverEntry{Command: "logos", Args: []string{"mcp", "serve"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	if strings.Contains(string(raw), "/old") || !strings.Contains(string(raw), `"disabled": true`) {
		t.Errorf("entry after unpinning:\n%s", raw)
	}
}
