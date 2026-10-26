package mcpserver

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/session"
)

// The checkpoint tool marks the verified line the session's log never saw run,
// as the CLI does, and the receipt that quotes the line quotes it masked: the
// mark is copied from verified before the commit masks it.
func TestTheCheckpointToolMarksAnUnseenVerifiedCommandAndQuotesItMasked(t *testing.T) {
	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	s := &Session{Server: &Server{DB: db, vault: vault}}
	proj := s.resolveScope("")
	// Another session ran go test, so the log knows test as one of go's; this
	// one only built.
	for _, e := range []activity.Event{
		{Kind: activity.KindTool, Project: proj, Session: "s0", Tool: "Bash", Summary: "Bash: go test ./..."},
		{Kind: activity.KindTool, Project: proj, Session: "s1", Tool: "Bash", Summary: "Bash: go build ./..."},
	} {
		if err := activity.Append(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	const key = "ghp_R2d2C3poBb8Ee9Ff0Gg1Hh2Ii3Jj4Kk5Ll6M"
	got, err := s.checkpoint(map[string]any{
		"agent": "claude", "task": "fix the upload", "next": "ship it",
		"verified": []any{"go test ./upload passes with GITHUB_TOKEN=" + key},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Marked unverified: 1 `verified` line") {
		t.Errorf("the receipt does not mark the line: %q", got)
	}
	if strings.Contains(got, key) {
		t.Errorf("the receipt repeated the token: %q", got)
	}
	c, err := session.Latest(vault, proj)
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if len(c.Unseen) != 1 || len(c.Verified) != 1 || c.Unseen[0] != c.Verified[0] {
		t.Errorf("the mark %q does not match the masked line %q", c.Unseen, c.Verified)
	}
}
