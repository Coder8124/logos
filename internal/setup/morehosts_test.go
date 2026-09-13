package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hostNamed finds a shipped host after HOME has been faked, since each host
// works out its paths when it is built.
func hostNamed(t *testing.T, name string) Host {
	t.Helper()
	for _, h := range Hosts() {
		if h.Name == name {
			return h
		}
	}
	t.Fatalf("no shipped host is called %q; brain knows: %s", name, strings.Join(Names(Hosts()), ", "))
	return Host{}
}

// fakeHome points HOME (and APPDATA) at a temp directory and makes the
// directories under it that mark a host as installed.
func fakeHome(t *testing.T, dirs ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("host paths here are the macOS and Linux ones")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func vscodeUserRel() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join("Library", "Application Support", "Code", "User")
	}
	return filepath.Join(".config", "Code", "User")
}

// installAndRead registers brain with h beside an existing server of the
// user's, and returns the file's top-level object.
func installAndRead(t *testing.T, h Host, root string) map[string]json.RawMessage {
	t.Helper()
	path := h.Config()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"` + root + `": {"theirs": {"command": "theirs"}}, "keepMe": 1}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if !h.Detect() {
		t.Fatalf("%s is not detected with its directory present", h.Name)
	}
	r := Install(server(), []Host{h})
	if r[0].Outcome != Updated && r[0].Outcome != Registered {
		t.Fatalf("%s: outcome %q (%v)", h.Name, r[0].Outcome, r[0].Err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("%s wrote invalid JSON: %v\n%s", h.Name, err, raw)
	}
	if _, ok := cfg["keepMe"]; !ok {
		t.Errorf("%s lost a key that was not ours:\n%s", h.Name, raw)
	}
	servers := map[string]map[string]any{}
	if err := json.Unmarshal(cfg[root], &servers); err != nil {
		t.Fatalf("%s has no %s object:\n%s", h.Name, root, raw)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Errorf("%s lost the user's other server:\n%s", h.Name, raw)
	}
	if servers[Name]["command"] != "/usr/local/bin/brain" {
		t.Errorf("%s: brain entry is %v", h.Name, servers[Name])
	}
	regs, err := h.List()
	if err != nil || len(regs) != 2 {
		t.Errorf("%s: List = %+v, %v; want both servers", h.Name, regs, err)
	}
	return cfg
}

func brainEntry(t *testing.T, cfg map[string]json.RawMessage, root string) map[string]any {
	t.Helper()
	servers := map[string]map[string]any{}
	if err := json.Unmarshal(cfg[root], &servers); err != nil {
		t.Fatal(err)
	}
	return servers[Name]
}

// Cline keeps its servers in the VS Code extension's global storage, in the
// same mcpServers shape Cursor uses. Setup could not wire it at all.
func TestSetupWiresClineInVSCode(t *testing.T) {
	fakeHome(t, filepath.Join(vscodeUserRel(), "globalStorage", "saoudrizwan.claude-dev"))
	installAndRead(t, hostNamed(t, "Cline"), "mcpServers")
}

// The Cline CLI's docs name ~/.cline/mcp.json, which the CLI never reads; its
// code reads data/settings/cline_mcp_settings.json.
func TestSetupWiresTheClineCLIWhereItActuallyReads(t *testing.T) {
	home := fakeHome(t, ".cline")
	h := hostNamed(t, "Cline CLI")
	if want := filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json"); h.Config() != want {
		t.Errorf("Cline CLI config = %s, want %s", h.Config(), want)
	}
	installAndRead(t, h, "mcpServers")
}

// `devin mcp add` defaults to a scope bound to one directory; the user scope
// file is the one that follows someone across projects.
func TestSetupWiresDevinAtUserScope(t *testing.T) {
	home := fakeHome(t, filepath.Join(".config", "devin"))
	h := hostNamed(t, "Devin")
	if want := filepath.Join(home, ".config", "devin", "mcp_config.json"); h.Config() != want {
		t.Errorf("Devin config = %s, want %s", h.Config(), want)
	}
	installAndRead(t, h, "mcpServers")
}

// Copilot CLI skips a server without a type, and exposes none of its tools
// without a tools list.
func TestSetupWiresCopilotCLIWithTheFieldsItRequires(t *testing.T) {
	home := fakeHome(t)
	path := filepath.Join(home, ".copilot", "mcp-config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := installAndRead(t, hostNamed(t, "Copilot CLI"), "mcpServers")
	e := brainEntry(t, cfg, "mcpServers")
	if e["type"] != "local" {
		t.Errorf("type = %v, want local", e["type"])
	}
	if tools, _ := e["tools"].([]any); len(tools) != 1 || tools[0] != "*" {
		t.Errorf("tools = %v, want [*]", e["tools"])
	}
	env, _ := e["env"].(map[string]any)
	if env["BRAIN_VAULT"] != "/Users/someone/brain" {
		t.Errorf("env = %v", e["env"])
	}
}

// A ~/.copilot directory alone is left by the VS Code extension, not the CLI;
// treating it as the CLI would write a config for a program that is not there.
func TestAnIDEOnlyCopilotDirectoryIsNotTheCopilotCLI(t *testing.T) {
	fakeHome(t, filepath.Join(".copilot", "ide"))
	t.Setenv("PATH", t.TempDir())
	if hostNamed(t, "Copilot CLI").Detect() {
		t.Error("a ~/.copilot/ide directory was taken as Copilot CLI being installed")
	}
}

// Copilot in VS Code reads the user mcp.json, whose map is "servers" and whose
// entries carry a type — the mcpServers shape there is not read at all.
func TestSetupWiresGitHubCopilotInVSCodeUnderServers(t *testing.T) {
	fakeHome(t, vscodeUserRel())
	cfg := installAndRead(t, hostNamed(t, "Copilot in VS Code"), "servers")
	if _, ok := cfg["mcpServers"]; ok {
		t.Error("wrote an mcpServers block VS Code does not read")
	}
	if e := brainEntry(t, cfg, "servers"); e["type"] != "stdio" {
		t.Errorf("type = %v, want stdio", e["type"])
	}
}

// A host name has to start with what people type for it: `--host copilot`
// matched nothing while the names began with "GitHub".
func TestHostCopilotSelectsACopilotHost(t *testing.T) {
	kept, unmatched := Only(Hosts(), []string{"copilot", "cline", "devin"})
	if len(unmatched) != 0 || len(kept) != 3 {
		t.Errorf("kept %v, unmatched %v", Names(kept), unmatched)
	}
}

// VS Code alone is not Copilot: someone who edits in VS Code and runs their
// agents elsewhere was offered, and with --yes given, an MCP entry for an
// assistant they do not have.
func TestVSCodeWithoutCopilotIsNotTakenAsCopilotInVSCode(t *testing.T) {
	home := fakeHome(t, vscodeUserRel())
	if hostNamed(t, "Copilot in VS Code").Detect() {
		t.Error("a VS Code settings directory alone was taken as Copilot in VS Code")
	}

	if err := os.MkdirAll(filepath.Join(home, ".vscode", "extensions", "github.copilot-chat-0.31.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !hostNamed(t, "Copilot in VS Code").Detect() {
		t.Error("the Copilot Chat extension is installed and was not detected")
	}
}

// A user mcp.json is VS Code's MCP already in use — the README's own button
// writes one — so it counts even where the extension lives somewhere else.
func TestAnExistingVSCodeMCPConfigCountsAsCopilotInVSCode(t *testing.T) {
	home := fakeHome(t, vscodeUserRel())
	if err := os.WriteFile(filepath.Join(home, vscodeUserRel(), "mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hostNamed(t, "Copilot in VS Code").Detect() {
		t.Error("a VS Code mcp.json was not taken as VS Code's MCP being in use")
	}
}
