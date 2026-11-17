package setup

import (
	"path/filepath"
	"testing"
)

// Each of these hosts reads a plain mcpServers block; the test is that the
// entry lands where the host reads it and the user's own keys survive.
func TestSetupWiresTheMcpServersJSONHosts(t *testing.T) {
	for _, c := range []struct{ name, rel string }{
		{"Gemini CLI", filepath.Join(".gemini", "settings.json")},
		{"Qwen Code", filepath.Join(".qwen", "settings.json")},
		{"Kiro", filepath.Join(".kiro", "settings", "mcp.json")},
		{"Windsurf", filepath.Join(".codeium", "windsurf", "mcp_config.json")},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolatePath(t)
			home := fakeHome(t, filepath.Dir(c.rel))
			h := hostNamed(t, c.name)
			if want := filepath.Join(home, c.rel); h.Config() != want {
				t.Errorf("%s config = %s, want %s", c.name, h.Config(), want)
			}
			installAndRead(t, h, "mcpServers")
		})
	}
}

func TestSetupWiresRooAndKiloCodeInTheirExtensionStorage(t *testing.T) {
	for name, ext := range map[string]string{"Roo Code": "rooveterinaryinc.roo-cline", "Kilo Code": "kilocode.kilo-code"} {
		isolatePath(t)
		fakeHome(t, filepath.Join(vscodeUserRel(), "globalStorage", ext))
		h := hostNamed(t, name)
		if filepath.Base(h.Config()) != "mcp_settings.json" {
			t.Errorf("%s config = %s", name, h.Config())
		}
		installAndRead(t, h, "mcpServers")
	}
}
