package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These files hold every MCP server the user has connected. Losing one of them
// to add ours would be a worse bug than never registering at all, so the merge
// gets the same treatment as the vault write paths: every hostile shape of
// input, and a hard requirement that existing content survives.

func server() Server {
	return Server{
		Bin:  "/usr/local/bin/brain",
		Args: []string{"mcp", "serve"},
		Env:  map[string]string{"BRAIN_VAULT": "/Users/someone/brain"},
	}
}

func readServers(t *testing.T, path string) map[string]serverEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var cfg struct {
		Servers map[string]serverEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("%s is not valid JSON after merge: %v\n%s", path, err, raw)
	}
	return cfg.Servers
}

func TestCreatesConfigWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	outcome, err := mergeJSON(path, server())
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Registered {
		t.Errorf("outcome = %q, want %q", outcome, Registered)
	}

	got := readServers(t, path)
	entry, ok := got[Name]
	if !ok {
		t.Fatalf("brain not in the written config: %+v", got)
	}
	if entry.Command != "/usr/local/bin/brain" {
		t.Errorf("command = %q, want an absolute path", entry.Command)
	}
	if entry.Env["BRAIN_VAULT"] != "/Users/someone/brain" {
		t.Errorf("BRAIN_VAULT = %q; a host launched from anywhere needs this absolute",
			entry.Env["BRAIN_VAULT"])
	}
}

// The one that matters: somebody else's servers must survive.
func TestExistingServersSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{
  "mcpServers": {
    "sentry": {"command": "npx", "args": ["-y", "@sentry/mcp"]},
    "postgres": {"command": "/opt/pg-mcp", "env": {"DSN": "postgres://localhost"}}
  },
  "someOtherSetting": {"theme": "dark"},
  "topLevelFlag": true
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeJSON(path, server()); err != nil {
		t.Fatal(err)
	}

	got := readServers(t, path)
	for _, name := range []string{"sentry", "postgres", Name} {
		if _, ok := got[name]; !ok {
			t.Errorf("%q is missing after the merge; other servers must survive", name)
		}
	}
	if got["sentry"].Command != "npx" {
		t.Errorf("sentry was rewritten: %+v", got["sentry"])
	}
	if got["postgres"].Env["DSN"] != "postgres://localhost" {
		t.Errorf("postgres lost its env: %+v", got["postgres"])
	}

	// Keys we know nothing about must come through untouched.
	raw, _ := os.ReadFile(path)
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(raw, &whole); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"someOtherSetting", "topLevelFlag"} {
		if _, ok := whole[key]; !ok {
			t.Errorf("unrelated top-level key %q was dropped", key)
		}
	}
}

func TestRerunUpdatesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	if _, err := mergeJSON(path, server()); err != nil {
		t.Fatal(err)
	}
	moved := server()
	moved.Env["BRAIN_VAULT"] = "/Users/someone/vaults/work"

	outcome, err := mergeJSON(path, moved)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Updated {
		t.Errorf("outcome = %q, want %q on a second run", outcome, Updated)
	}

	got := readServers(t, path)
	if len(got) != 1 {
		t.Errorf("re-running produced %d entries, want 1 — it must update, not duplicate", len(got))
	}
	if got[Name].Env["BRAIN_VAULT"] != "/Users/someone/vaults/work" {
		t.Errorf("the vault path did not update: %+v", got[Name])
	}
}

// setup is run more than once — after moving a vault, after an update, or just
// to check things are still wired. Backing up a file the second run leaves
// byte-for-byte identical is litter left behind in the user's own config
// directory on every re-run, forever.
func TestRerunWithNoChangeLeavesNoBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	if _, err := mergeJSON(path, server()); err != nil {
		t.Fatal(err)
	}
	// The first run's own backup-of-nothing must not linger either, so remove
	// it before asserting on the re-run in isolation.
	os.Remove(path + ".brain-backup")

	outcome, err := mergeJSON(path, server())
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Unchanged {
		t.Errorf("outcome = %q, want %q when nothing about the entry changed", outcome, Unchanged)
	}
	if _, err := os.Stat(path + ".brain-backup"); err == nil {
		t.Error("a backup was written even though the re-run changed nothing")
	}
}

// A file we cannot parse is a file we must not replace.
func TestMalformedConfigIsRefusedNotClobbered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	broken := `{"mcpServers": {"sentry": {"command": "npx"` // truncated
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	outcome, err := mergeJSON(path, server())
	if err == nil {
		t.Error("a malformed config was overwritten instead of refused")
	}
	if outcome != Failed {
		t.Errorf("outcome = %q, want %q", outcome, Failed)
	}

	after, _ := os.ReadFile(path)
	if string(after) != broken {
		t.Errorf("the user's file was modified despite the refusal:\n%s", after)
	}
}

func TestBackupIsWrittenBeforeChanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{"mcpServers": {"sentry": {"command": "npx"}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeJSON(path, server()); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".brain-backup")
	if err != nil {
		t.Fatalf("no backup was written: %v", err)
	}
	if string(backup) != original {
		t.Errorf("the backup does not match what was there before:\n%s", backup)
	}
}

// An empty file is what an editor leaves behind, and is not malformed.
func TestEmptyConfigIsTreatedAsNew(t *testing.T) {
	for name, content := range map[string]string{
		"empty":         "",
		"whitespace":    "\n\n  \n",
		"bare object":   "{}",
		"empty servers": `{"mcpServers": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := mergeJSON(path, server()); err != nil {
				t.Fatalf("%s config was refused: %v", name, err)
			}
			if _, ok := readServers(t, path)[Name]; !ok {
				t.Error("brain was not registered")
			}
		})
	}
}

// Install must report rather than throw when a host is absent, and must keep
// going after one host fails.
func TestInstallReportsEveryHost(t *testing.T) {
	dir := t.TempDir()
	hosts := []Host{
		{
			Name:   "present",
			Detect: func() bool { return true },
			Where:  func() string { return filepath.Join(dir, "a.json") },
			Register: func(s Server) (Outcome, error) {
				return mergeJSON(filepath.Join(dir, "a.json"), s)
			},
		},
		{
			Name:     "absent",
			Detect:   func() bool { return false },
			Where:    func() string { return "nowhere" },
			Register: func(Server) (Outcome, error) { t.Fatal("must not register an absent host"); return Failed, nil },
		},
		{
			Name:     "broken",
			Detect:   func() bool { return true },
			Where:    func() string { return "somewhere" },
			Register: func(Server) (Outcome, error) { return Failed, os.ErrPermission },
		},
	}

	results := Install(server(), hosts)
	if len(results) != 3 {
		t.Fatalf("want a line per host, got %d", len(results))
	}
	want := map[string]Outcome{"present": Registered, "absent": Skipped, "broken": Failed}
	for _, r := range results {
		if r.Outcome != want[r.Host] {
			t.Errorf("%s: outcome = %q, want %q", r.Host, r.Outcome, want[r.Host])
		}
	}
	// The failure must not have stopped the host after it.
	if _, ok := readServers(t, filepath.Join(dir, "a.json"))[Name]; !ok {
		t.Error("the working host was not registered")
	}
}

// Plan is what the user is shown before agreeing to anything, so the one
// property that matters is that looking does not touch.
func TestPlanWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")

	hosts := []Host{{
		Name:     "present",
		Detect:   func() bool { return true },
		Where:    func() string { return path },
		Register: func(Server) (Outcome, error) { t.Fatal("Plan must not register"); return Failed, nil },
	}, {
		Name:     "absent",
		Detect:   func() bool { return false },
		Where:    func() string { return "nowhere" },
		Register: func(Server) (Outcome, error) { t.Fatal("Plan must not register"); return Failed, nil },
	}}

	results := Plan(hosts)
	want := map[string]Outcome{"present": Pending, "absent": Skipped}
	for _, r := range results {
		if r.Outcome != want[r.Host] {
			t.Errorf("%s: outcome = %q, want %q", r.Host, r.Outcome, want[r.Host])
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Plan created a config file; it must only look")
	}
}

// A user evaluating one integration types the host name however it occurs to
// them. All of these should reach Claude Code.
func TestOnlyMatchesHowPeopleType(t *testing.T) {
	hosts := Hosts()
	for _, spelling := range []string{"claude-code", "Claude Code", "claudecode", "CLAUDE_CODE", "claude c"} {
		kept, unmatched := Only(hosts, []string{spelling})
		if len(unmatched) > 0 {
			t.Errorf("%q was not matched to a host", spelling)
			continue
		}
		if len(kept) != 1 || kept[0].Name != "Claude Code" {
			t.Errorf("%q selected %v, want just Claude Code", spelling, Names(kept))
		}
	}
}

// An unknown name must be reported, never silently ignored — wiring nothing
// while claiming success is the failure mode being avoided.
func TestOnlyReportsUnknownNames(t *testing.T) {
	kept, unmatched := Only(Hosts(), []string{"cursor", "emacs"})
	if len(unmatched) != 1 || unmatched[0] != "emacs" {
		t.Errorf("unmatched = %v, want [emacs]", unmatched)
	}
	if len(kept) != 1 || kept[0].Name != "Cursor" {
		t.Errorf("kept = %v, want [Cursor]", Names(kept))
	}
}

// No --host means every host, which is the behaviour setup has always had.
func TestOnlyWithNoNamesKeepsEverything(t *testing.T) {
	kept, unmatched := Only(Hosts(), nil)
	if len(unmatched) != 0 {
		t.Errorf("unmatched = %v, want none", unmatched)
	}
	if len(kept) != len(Hosts()) {
		t.Errorf("kept %d hosts, want all %d", len(kept), len(Hosts()))
	}
}

// The hosts we ship must at least be well-formed: named, and able to answer
// where they live without panicking on a machine that has none of them.
func TestShippedHostsAreWellFormed(t *testing.T) {
	for _, h := range Hosts() {
		if strings.TrimSpace(h.Name) == "" {
			t.Error("a host has no name")
		}
		if h.Detect == nil || h.Where == nil || h.Register == nil {
			t.Errorf("%s is missing a function", h.Name)
		}
		h.Detect()
		h.Where()
	}
}

// parseClaudeMCPList reads a report meant for a person, not a parser — the
// same bet Register already makes matching "already exists" in this same
// command's stdout. A banner line and a blank line must not be mistaken for
// servers, and a server whose command happens to contain extra colons or
// spaces must still come back whole.
func TestParseClaudeMCPListSkipsNonServerLines(t *testing.T) {
	out := "Checking MCP server health…\n\n" +
		"claude.ai Google Drive: https://drivemcp.googleapis.com/mcp/v1 - ✔ Connected\n" +
		"plugin:playwright:playwright: npx @playwright/mcp@latest - ✔ Connected\n" +
		"plugin:logos:logos: /Users/someone/.claude/plugins/cache/logos/logos/0.1.2/bin/mcp.sh  - ✔ Connected\n" +
		"brain: /usr/local/bin/brain mcp serve - ✔ Connected\n"

	regs := parseClaudeMCPList([]byte(out))
	want := map[string]string{
		"claude.ai Google Drive":       "https://drivemcp.googleapis.com/mcp/v1",
		"plugin:playwright:playwright": "npx @playwright/mcp@latest",
		"plugin:logos:logos":           "/Users/someone/.claude/plugins/cache/logos/logos/0.1.2/bin/mcp.sh",
		"brain":                        "/usr/local/bin/brain mcp serve",
	}
	if len(regs) != len(want) {
		t.Fatalf("got %d registrations, want %d: %+v", len(regs), len(want), regs)
	}
	for _, r := range regs {
		if want[r.Name] != r.Command {
			t.Errorf("%s: command = %q, want %q", r.Name, r.Command, want[r.Name])
		}
	}
}

func TestParseClaudeMCPListOnEmptyOutputReturnsNothing(t *testing.T) {
	if regs := parseClaudeMCPList([]byte("")); regs != nil {
		t.Errorf("want nil for no servers, got %+v", regs)
	}
}

// readMCPServers is the other half of List: a JSON-config host reading back
// what mergeJSON itself writes, so a check built on it fails closed if the two
// ever disagree about the shape.
func TestReadMCPServersRoundTripsWhatMergeJSONWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := mergeJSON(path, server()); err != nil {
		t.Fatal(err)
	}

	regs, err := readMCPServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 1 || regs[0].Name != Name {
		t.Fatalf("got %+v, want one registration named %q", regs, Name)
	}
	if regs[0].Command != "/usr/local/bin/brain mcp serve" {
		t.Errorf("command = %q", regs[0].Command)
	}
}

// A file with more than one server in it — the shape a real machine has,
// since brain is never the first thing anyone points an MCP host at.
func TestReadMCPServersReadsEveryEntryNotJustBrains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	raw := `{"mcpServers": {
		"brain": {"command": "/usr/local/bin/brain", "args": ["mcp", "serve"]},
		"some-other-server": {"command": "npx", "args": ["some-other-mcp"]}
	}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	regs, err := readMCPServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 {
		t.Fatalf("got %d registrations, want 2: %+v", len(regs), regs)
	}
}

func TestReadMCPServersOnAMissingFileIsNotAnError(t *testing.T) {
	regs, err := readMCPServers(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil || regs != nil {
		t.Errorf("readMCPServers on an absent file = %+v, %v; want nil, nil", regs, err)
	}
}

// RenderConfig exists for MCP clients brain does not know how to find or
// register — anything outside the four hosts in Hosts(). Those users are
// real, and until this existed the only answer for them was "not supported":
// no way to see what a working config even looks like, so no way to type one
// in by hand.

func TestRenderConfigJSONIsWhatMergeJSONWouldHaveWritten(t *testing.T) {
	s := server()
	out, err := RenderConfig(s, "json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]serverEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("--print-config --format json produced invalid JSON: %v\n%s", err, out)
	}
	got, ok := cfg.Servers[Name]
	if !ok {
		t.Fatalf("no %q entry in the printed config:\n%s", Name, out)
	}
	if got.Command != s.Bin || got.Env["BRAIN_VAULT"] != s.Env["BRAIN_VAULT"] {
		t.Errorf("printed entry = %+v, want the same server mergeJSON would have written", got)
	}
}

// json is the implicit default: the same shape Claude Desktop and Cursor
// already use, so copying it into either config file by hand just works.
func TestRenderConfigDefaultsToJSON(t *testing.T) {
	withFormat, err := RenderConfig(server(), "json")
	if err != nil {
		t.Fatal(err)
	}
	withoutFormat, err := RenderConfig(server(), "")
	if err != nil {
		t.Fatal(err)
	}
	if withFormat != withoutFormat {
		t.Errorf("an empty format produced a different render than an explicit \"json\":\n%s\nvs\n%s", withoutFormat, withFormat)
	}
}

func TestRenderConfigTOMLNamesTheServerTableAndItsEnv(t *testing.T) {
	s := server()
	out, err := RenderConfig(s, "toml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[mcp_servers.brain]") {
		t.Errorf("toml output has no [mcp_servers.brain] table:\n%s", out)
	}
	if !strings.Contains(out, `command = "`+s.Bin+`"`) {
		t.Errorf("toml output does not name the binary as command:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.brain.env]") ||
		!strings.Contains(out, `BRAIN_VAULT = "`+s.Env["BRAIN_VAULT"]+`"`) {
		t.Errorf("toml output does not carry BRAIN_VAULT in an env table:\n%s", out)
	}
}

func TestRenderConfigRejectsAnUnknownFormat(t *testing.T) {
	if _, err := RenderConfig(server(), "yaml"); err == nil {
		t.Error("RenderConfig(..., \"yaml\") = nil error, want a complaint naming the formats it does know")
	}
}

// MergeFile is mergeJSON, exported for `brain setup --config <path>` — a host
// whose location brain does not know by convention, but whose file is the same
// mcpServers-keyed JSON Claude Desktop and Cursor already read.
func TestMergeFileIsMergeJSONExported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom-host.json")
	outcome, err := MergeFile(path, server())
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Registered {
		t.Errorf("outcome = %q, want %q for a file that did not exist yet", outcome, Registered)
	}
	got := readServers(t, path)
	if _, ok := got[Name]; !ok {
		t.Errorf("%s was not written into %s", Name, path)
	}
}
