package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatePath keeps a real opencode, amp or grok on this machine's PATH from
// deciding what a test detects.
func isolatePath(t *testing.T) { t.Setenv("PATH", t.TempDir()) }

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// opencode starts only servers under "mcp" that are typed and carry the command
// and its arguments as one array. An mcpServers entry would sit in the file and
// never run.
func TestSetupWiresOpencodeUnderMcpAsATypedLocalCommand(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, filepath.Join(".config", "opencode"))
	h := hostNamed(t, "opencode")
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if h.Config() != path {
		t.Fatalf("opencode config = %s, want %s", h.Config(), path)
	}
	writeFile(t, path, `{"$schema": "https://opencode.ai/config.json", "mcp": {"theirs": {"type": "local", "command": ["theirs", "serve"], "enabled": true}}}`)

	if r := Install(server(), []Host{h}); r[0].Outcome != Registered {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	cfg := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(readFile(t, path)), &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["$schema"]; !ok {
		t.Error("the user's $schema was lost")
	}
	servers := map[string]opencodeEntry{}
	if err := json.Unmarshal(cfg["mcp"], &servers); err != nil {
		t.Fatal(err)
	}
	got := servers[Name]
	if got.Type != "local" || !got.Enabled || strings.Join(got.Command, " ") != "/usr/local/bin/logos mcp serve" || got.Environment["LOGOS_VAULT"] != "/Users/someone/logos" {
		t.Errorf("logos entry = %+v", got)
	}
	if _, ok := servers["theirs"]; !ok {
		t.Error("the user's other server was lost")
	}

	regs, err := h.List()
	if err != nil || len(regs) != 2 {
		t.Fatalf("List = %+v, %v; want both servers", regs, err)
	}
	for _, r := range regs {
		if r.Name == Name && (r.Command != "/usr/local/bin/logos mcp serve" || r.Vault != "/Users/someone/logos") {
			t.Errorf("logos reads back as %+v", r)
		}
	}
}

// A config kept as opencode.jsonc is the one written to, and the comments a
// rewrite drops are reported as surviving only in the backup.
func TestOpencodeIsWiredInItsJSONCFileWhenThatIsTheOneThere(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, filepath.Join(".config", "opencode"))
	dir := filepath.Join(home, ".config", "opencode")
	writeFile(t, filepath.Join(dir, "opencode.jsonc"), "{\n  // the local model\n  \"model\": \"ollama/qwen\"\n}\n")
	h := hostNamed(t, "opencode")
	if h.Config() != filepath.Join(dir, "opencode.jsonc") {
		t.Fatalf("opencode config = %s, want the .jsonc beside it", h.Config())
	}

	r := Install(server(), []Host{h})
	if r[0].Outcome != Registered {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	if !r[0].CommentsOnlyInBackup {
		t.Error("the dropped comment was not reported")
	}
	if exists(filepath.Join(dir, "opencode.json")) {
		t.Error("a second config was created beside the user's .jsonc")
	}
	if !strings.Contains(readFile(t, h.Config()), `"model": "ollama/qwen"`) {
		t.Errorf("the user's model setting was lost:\n%s", readFile(t, h.Config()))
	}
}

// Amp's settings file keys its servers "amp.mcpServers" at the top level — one
// dotted name, not an "amp" object holding mcpServers.
func TestSetupWiresAmpUnderItsDottedKey(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, filepath.Join(".config", "amp"))
	h := hostNamed(t, "Amp")
	if want := filepath.Join(home, ".config", "amp", "settings.json"); h.Config() != want {
		t.Errorf("Amp config = %s, want %s", h.Config(), want)
	}
	cfg := installAndRead(t, h, "amp.mcpServers")
	if _, nested := cfg["amp"]; nested {
		t.Error("wrote a nested amp object, which Amp does not read")
	}
}

const grokConfig = `# my grok
model = "grok-code"

[mcp_servers.theirs]
command = "theirs"
args = ["serve"]

[mcp_servers.logos]
command = "/old/logos"
args = ["mcp", "serve"]

[mcp_servers.logos.env]
LOGOS_VAULT = "/old/vault"

[compat.claude]
mcps = true
`

// Grok Build's config.toml holds its whole configuration. Re-running setup
// replaces logos's own tables and nothing else, comments included.
func TestSetupRewritesGrokBuildsLogosTableAndKeepsEverythingElse(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, ".grok")
	path := filepath.Join(home, ".grok", "config.toml")
	writeFile(t, path, grokConfig)
	h := hostNamed(t, "Grok Build")
	if !h.Detect() {
		t.Fatal("Grok Build with a config.toml was not detected")
	}

	if r := Install(server(), []Host{h}); r[0].Outcome != Updated {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	got := readFile(t, path)
	for _, keep := range []string{"# my grok", `model = "grok-code"`, "[mcp_servers.theirs]", "[compat.claude]", "mcps = true"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	if n := strings.Count(got, "[mcp_servers.logos]"); n != 1 {
		t.Errorf("%d logos tables, want 1:\n%s", n, got)
	}
	if strings.Contains(got, "/old/") {
		t.Errorf("the old entry survived:\n%s", got)
	}
	regs, err := h.List()
	if err != nil || len(regs) != 2 {
		t.Fatalf("List = %+v, %v; want both servers", regs, err)
	}
	for _, r := range regs {
		if r.Name == Name && (r.Command != "/usr/local/bin/logos mcp serve" || r.Vault != "/Users/someone/logos") {
			t.Errorf("logos reads back as %+v", r)
		}
	}
}

// A second [mcp_servers.logos] beside an inline mcp_servers table is a file
// Grok will not load, every server in it lost with ours.
func TestAGrokConfigWithServersInAFormLogosCannotEditIsLeftAlone(t *testing.T) {
	isolatePath(t)
	for name, body := range map[string]string{
		"inline":     "mcp_servers = { theirs = { command = \"theirs\" } }\n",
		"dotted":     "mcp_servers.theirs.command = \"theirs\"\n",
		"bare table": "[mcp_servers]\ntheirs = { command = \"theirs\" }\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := fakeHome(t, ".grok")
			path := filepath.Join(home, ".grok", "config.toml")
			writeFile(t, path, body)
			r := Install(server(), []Host{hostNamed(t, "Grok Build")})
			if r[0].Outcome != Failed || r[0].Err == nil || !strings.Contains(r[0].Err.Error(), "--print-config") {
				t.Errorf("outcome %q (%v), want a refusal naming --print-config", r[0].Outcome, r[0].Err)
			}
			if readFile(t, path) != body {
				t.Errorf("the refused file was changed:\n%s", readFile(t, path))
			}
		})
	}
}

// GROK_HOME moves Grok Build's config with the rest of its home.
func TestGrokBuildIsWiredUnderGROK_HOMEWhenItIsSet(t *testing.T) {
	isolatePath(t)
	fakeHome(t)
	dir := t.TempDir()
	t.Setenv("GROK_HOME", dir)
	if err := os.Mkdir(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := hostNamed(t, "Grok Build")
	if h.Config() != filepath.Join(dir, "config.toml") || !h.Detect() {
		t.Fatalf("config %s, detected %v; want %s", h.Config(), h.Detect(), filepath.Join(dir, "config.toml"))
	}
	if r := Install(server(), []Host{h}); r[0].Outcome != Registered {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
}

// The community grok-cli keeps its settings in ~/.grok as well. A directory
// holding only its user-settings.json is not Grok Build.
func TestACommunityGrokCLIDirectoryIsNotGrokBuild(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, ".grok")
	writeFile(t, filepath.Join(home, ".grok", "user-settings.json"), "{}")
	if hostNamed(t, "Grok Build").Detect() {
		t.Error("the community grok-cli's directory was taken for Grok Build")
	}
}

// Uninstall takes logos out of each file and leaves the user's own servers.
func TestUninstallTakesOnlyLogosOutOfOpencodeAmpAndGrokBuild(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, filepath.Join(".config", "opencode"), filepath.Join(".config", "amp"), ".grok")
	writeFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"mcp": {"theirs": {"type": "local", "command": ["theirs"], "enabled": true}}}`)
	writeFile(t, filepath.Join(home, ".config", "amp", "settings.json"), `{"amp.mcpServers": {"theirs": {"command": "theirs"}}}`)
	writeFile(t, filepath.Join(home, ".grok", "config.toml"), "[mcp_servers.theirs]\ncommand = \"theirs\"\n")
	hosts := []Host{hostNamed(t, "opencode"), hostNamed(t, "Amp"), hostNamed(t, "Grok Build")}
	for _, r := range Install(server(), hosts) {
		if r.Outcome != Registered {
			t.Fatalf("%s: outcome %q (%v)", r.Host, r.Outcome, r.Err)
		}
	}

	for _, h := range hosts {
		removed, err := h.Remove(Name)
		if err != nil || !removed {
			t.Errorf("%s: Remove = %v, %v", h.Name, removed, err)
		}
		regs, err := h.List()
		if err != nil || len(regs) != 1 || regs[0].Name != "theirs" {
			t.Errorf("%s: after removal List = %+v, %v; want only theirs", h.Name, regs, err)
		}
	}
}

// The community grok-cli installs a `grok` command too. A machine with only
// that one must not be told Grok Build was registered, in a config.toml
// nothing reads and that then makes every later run think Grok Build is there.
func TestAGrokCommandAloneIsNotGrokBuild(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	writeFile(t, filepath.Join(bin, "grok"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := fakeHome(t)
	if hostNamed(t, "Grok Build").Detect() {
		t.Error("a bare grok command was taken for Grok Build")
	}
	if r := Install(server(), []Host{hostNamed(t, "Grok Build")}); r[0].Outcome != Skipped {
		t.Errorf("outcome %q, want skipped", r[0].Outcome)
	}
	if exists(filepath.Join(home, ".grok", "config.toml")) {
		t.Error("setup created a config.toml for a Grok Build that is not installed")
	}
}

// A comment the user put above the table after logos's belongs to that table.
// Re-running setup replaces logos's table, not the note on the next one.
func TestRerunningSetupKeepsTheCommentAboveTheTableAfterLogos(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, ".grok")
	path := filepath.Join(home, ".grok", "config.toml")
	writeFile(t, path, `model = "grok-code"

[mcp_servers.logos]
command = "/old/logos"
args = ["mcp", "serve"]

[mcp_servers.logos.env]
LOGOS_VAULT = "/old/vault"

# rotates monthly
[mcp_servers.github]
command = "github-mcp"
`)
	if r := Install(server(), []Host{hostNamed(t, "Grok Build")}); r[0].Outcome != Updated {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "# rotates monthly\n[mcp_servers.github]") {
		t.Errorf("the comment on the user's github table was lost:\n%s", got)
	}
	if strings.Contains(got, "/old/") {
		t.Errorf("the old entry survived:\n%s", got)
	}
}
