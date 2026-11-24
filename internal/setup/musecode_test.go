package setup

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// `muse mcp add --user` writes a typed stdio entry under mcpServers in
// ~/.config/muse/settings.json, and leaves its schema_version alone.
func TestSetupWiresMuseCodeAsATypedStdioServerAndKeepsItsSchemaVersion(t *testing.T) {
	isolatePath(t)
	home := fakeHome(t, filepath.Join(".config", "muse"))
	h := hostNamed(t, "Muse Code")
	if want := filepath.Join(home, ".config", "muse", "settings.json"); h.Config() != want {
		t.Fatalf("Muse Code config = %s, want %s", h.Config(), want)
	}
	writeFile(t, h.Config(), `{"mcpServers":{"theirs":{"type":"stdio","command":"theirs"}},"schema_version":1}`)
	if r := Install(server(), []Host{h}); r[0].Outcome != Registered && r[0].Outcome != Updated {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	var cfg struct {
		Servers map[string]map[string]any `json:"mcpServers"`
		Schema  int                       `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(readFile(t, h.Config())), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Schema != 1 || cfg.Servers["theirs"] == nil {
		t.Errorf("the user's own settings were lost: %+v", cfg)
	}
	got := cfg.Servers[Name]
	if got["type"] != "stdio" || !strings.Contains(got["command"].(string), "logos") {
		t.Errorf("logos entry = %v, want a stdio entry running logos", got)
	}
}

// `muse exec` refuses a settings.json with no schema_version, so a file logos
// creates from nothing has to carry it.
func TestAMuseCodeSettingsFileCreatedBySetupCarriesItsSchemaVersion(t *testing.T) {
	isolatePath(t)
	fakeHome(t, filepath.Join(".config", "muse"))
	h := hostNamed(t, "Muse Code")
	if r := Install(server(), []Host{h}); r[0].Outcome != Registered {
		t.Fatalf("outcome %q (%v)", r[0].Outcome, r[0].Err)
	}
	if body := readFile(t, h.Config()); !strings.Contains(body, `"schema_version": 1`) {
		t.Errorf("a fresh Muse settings file has no schema_version, so muse exec rejects it:\n%s", body)
	}
}
