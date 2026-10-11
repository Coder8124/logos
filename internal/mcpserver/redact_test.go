package mcpserver

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// The masking is invisible from the vault's side — the file just has a marker
// in it. The receipt is the only place the agent that pasted the key learns it
// did, so each write tool has to say so.
func TestEveryWriteToolSaysWhenItMaskedACredential(t *testing.T) {
	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	s := &Session{Server: &Server{DB: db, vault: t.TempDir()}}
	const key = "ghp_R2d2C3poBb8Ee9Ff0Gg1Hh2Ii3Jj4Kk5Ll6M"

	receipts := map[string]string{}
	var err error
	if receipts["remember"], _, err = s.remember("CI reads GITHUB_TOKEN="+key, "fact", "", false); err != nil {
		t.Fatal(err)
	}
	if receipts["note_progress"], err = s.noteProgress("kestrel", "claude", "pushed with "+key); err != nil {
		t.Fatal(err)
	}
	if receipts["checkpoint"], err = s.checkpoint(map[string]any{
		"agent": "claude", "task": "fix the upload",
		"verified": []any{"upload works with GITHUB_TOKEN=" + key},
	}, ""); err != nil {
		t.Fatal(err)
	}
	for tool, got := range receipts {
		if !strings.Contains(got, "redacted before writing") {
			t.Errorf("%s did not say it masked the token: %q", tool, got)
		}
		if strings.Contains(got, key) {
			t.Errorf("%s repeated the token in its receipt: %q", tool, got)
		}
	}
}
