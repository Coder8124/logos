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
