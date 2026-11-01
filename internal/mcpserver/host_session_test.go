package mcpserver

import (
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/session"
)

func hostSessionFixture(t *testing.T, client string, events ...activity.Event) (*Session, string) {
	t.Helper()
	t.Setenv("LOGOS_TRUST_MCP", "1")
	t.Setenv("LOGOS_PROJECT", "acme-api")
	db := testDB(t)
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	s := &Session{Server: &Server{DB: db, vault: vault}, clientAgent: client}
	proj := s.resolveScope("")
	for _, e := range events {
		e.Project = proj
		if err := activity.Append(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	return s, proj
}

// Only Claude Code's hooks write the activity log, so a host with none —
// opencode always, Codex until its hooks run — found Claude's conversation as
// the newest event and filed its checkpoint in Claude's conversation folder,
// where anything keyed by host session took it for Claude's own.
func TestACheckpointFromAHostWithNoHooksIsNotFiledUnderAnotherHostsConversation(t *testing.T) {
	s, proj := hostSessionFixture(t, "codex", activity.Event{
		Kind: activity.KindTool, Agent: "claude-code", Session: "1e72cd2e",
		Extra: map[string]any{"session_id": "1e72cd2e-claude-conversation"},
	})
	if _, err := s.checkpoint(map[string]any{"task": "rename the route", "next": "ship it"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := session.Latest(s.vault, proj)
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if c.HostSession != "" {
		t.Errorf("codex's checkpoint took Claude's conversation %q", c.HostSession)
	}
}

// The host's own events still name its conversation, which is what the
// per-conversation folders are for.
func TestACheckpointFromAHookedHostIsFiledUnderItsConversation(t *testing.T) {
	s, proj := hostSessionFixture(t, "claude-code", activity.Event{
		Kind: activity.KindTool, Agent: "claude-code", Session: "1e72cd2e",
		Extra: map[string]any{"session_id": "1e72cd2e-claude-conversation"},
	})
	if _, err := s.checkpoint(map[string]any{"task": "rename the route", "next": "ship it"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := session.Latest(s.vault, proj)
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if c.HostSession != "1e72cd2e-claude-conversation" {
		t.Errorf("host session %q, want Claude's own conversation", c.HostSession)
	}
}

// Read orders by whole-second time and keeps file order within a second, so
// newest-first put the first event written in the newest second ahead of the
// last one, and two conversations logging in one second filed the checkpoint
// under whichever wrote first.
func TestTheLastEventWrittenInTheNewestSecondNamesTheConversation(t *testing.T) {
	now := time.Now().Unix()
	s, _ := hostSessionFixture(t, "claude-code",
		activity.Event{TS: now, Kind: activity.KindTool, Agent: "claude-code", Extra: map[string]any{"session_id": "other-window"}},
		activity.Event{TS: now, Kind: activity.KindTool, Agent: "claude-code", Extra: map[string]any{"session_id": "this-window"}},
	)
	if got := s.hostSession(s.resolveScope("")); got != "this-window" {
		t.Errorf("host session %q, want the one written last", got)
	}
}
