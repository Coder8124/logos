package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// VS Code's mcp.json is JSONC, and the README's own "Add to VS Code" button
// writes the kind of file this meets. Setup refused it as "not valid JSON" and
// told the user to fix a file that was not broken.
func TestAHostConfigWithCommentsAndTrailingCommasIsMergedNotRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{
  // added by the VS Code button
  "servers": {
    /* a server the user already had */
    "docs": {"type": "http", "url": "https://example.com/mcp//x",},
  },
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts := []Host{{
		Name:   "Copilot in VS Code",
		Detect: func() bool { return true },
		Where:  func() string { return path },
		Config: func() string { return path },
		Register: func(s Server) (Outcome, error) {
			return mergeServers(path, "servers", serverEntry{Command: s.Bin, Args: s.Args})
		},
	}}

	r := Install(server(), hosts)
	if r[0].Outcome != Registered {
		t.Fatalf("outcome = %q (%v), want the commented config merged", r[0].Outcome, r[0].Err)
	}

	var cfg struct {
		Servers map[string]map[string]any `json:"servers"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("the rewritten config is not JSON: %v\n%s", err, raw)
	}
	if cfg.Servers["docs"]["url"] != "https://example.com/mcp//x" {
		t.Errorf("the user's own server was damaged: %v", cfg.Servers["docs"])
	}
	if _, ok := cfg.Servers[Name]; !ok {
		t.Errorf("brain was not added:\n%s", raw)
	}
	// The rewrite cannot keep the comments, so the report has to say where
	// they went rather than let them vanish without a word.
	if !r[0].CommentsOnlyInBackup {
		t.Error("the result does not say the original's comments survive only in the backup")
	}

	regs, err := readServerBlock(path+".brain-backup", "servers")
	if err != nil || len(regs) != 1 {
		t.Errorf("reading back the commented original gave %v, %v; want the one server it holds", regs, err)
	}
}
