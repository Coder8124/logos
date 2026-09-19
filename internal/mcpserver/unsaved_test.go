package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// testServer is a server over an empty scratch vault. The close path writes
// checkpoint files and reads them back, so it needs no database.
func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	vault := t.TempDir()
	return New(nil, nil, vault), vault
}

// workedIn is a transcript of a session in project that edited a file and ran a
// command, ending now — the shape that has something to record.
func workedIn(harness, project string) *transcript.Session {
	return &transcript.Session{
		Harness: harness,
		ID:      "sess-1",
		Project: project,
		Started: time.Now().Add(-time.Hour).Unix(),
		Ended:   time.Now().Unix(),
		Turns: []transcript.Turn{
			{Role: "user", Text: "fix the checkout crash when cart is empty"},
			{Role: "tool", Tool: "edit_file", Input: "internal/cart/checkout.go"},
			{Role: "tool", Tool: "run_terminal_cmd", Input: "go test ./internal/cart", Status: "ok"},
		},
	}
}

// fakeTranscripts points the close path at a transcript of our own instead of
// whatever this machine's Cursor or Codex happens to hold.
func fakeTranscripts(t *testing.T, s *transcript.Session) {
	t.Helper()
	old := newestSession
	newestSession = func(harness string) (*transcript.Session, error) {
		if s == nil || s.Harness != harness {
			return nil, nil
		}
		return s, nil
	}
	t.Cleanup(func() { newestSession = old })
}

// The whole of #31's remaining half: a host with no Stop hook never tells Logos
// the session ended, so a session that forgot to checkpoint was lost outright
// and the next resume showed nothing. The host's own transcript is the one
// record that does not need the host's cooperation.
func TestASessionThatNeverCheckpointedIsRecordedWhenTheHostCloses(t *testing.T) {
	srv, vault := testServer(t)
	sess := &Session{Server: srv, clientAgent: "cursor"}
	fakeTranscripts(t, workedIn("cursor", "shop"))

	sess.recordUnsaved()

	c, err := session.Latest(vault, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("the session closed with work done and nothing was recorded")
	}
	if !c.Auto {
		t.Errorf("the record is not marked auto, so it reads as an agent's own")
	}
	if !strings.Contains(c.State, "cursor") {
		t.Errorf("state = %q, want it to name where the record came from", c.State)
	}
}

// An agent that checkpointed said what it verified and what it ruled out. A
// mechanical record written on top of that would outrank it by being newer,
// and would replace a reviewed handoff with a list of files.
func TestAnAgentsOwnCheckpointIsNotOverwrittenWhenTheHostCloses(t *testing.T) {
	srv, vault := testServer(t)
	sess := &Session{Server: srv, clientAgent: "cursor"}
	sess.checkpointed("shop")
	fakeTranscripts(t, workedIn("cursor", "shop"))

	sess.recordUnsaved()

	c, err := session.Latest(vault, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("an auto record was written over an agent's own checkpoint: %s", c.Slug)
	}
}

// Claude Code has hooks, and its session-end path already writes this record
// from the activity log. Doing it here too would write the session twice.
func TestAHostWithItsOwnHooksIsLeftToThem(t *testing.T) {
	srv, vault := testServer(t)
	sess := &Session{Server: srv, clientAgent: "claude-code"}
	fakeTranscripts(t, workedIn("claude-code", "shop"))

	sess.recordUnsaved()

	c, err := session.Latest(vault, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("claude-code was recorded here as well as by its own hooks: %s", c.Slug)
	}
}

// The newest transcript on disk is not necessarily this session's: a host the
// user closed an hour ago left one too, and harvesting that would attribute
// somebody else's work to a session that did nothing.
func TestATranscriptThatEndedBeforeThisServerStartedIsNotRecorded(t *testing.T) {
	srv, vault := testServer(t)
	srv.startedAt = time.Now()
	sess := &Session{Server: srv, clientAgent: "cursor"}

	stale := workedIn("cursor", "shop")
	stale.Started = time.Now().Add(-4 * time.Hour).Unix()
	stale.Ended = time.Now().Add(-3 * time.Hour).Unix()
	fakeTranscripts(t, stale)

	sess.recordUnsaved()

	c, err := session.Latest(vault, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("a transcript that closed before this server started was recorded as its session: %s", c.Slug)
	}
}

// The same per-project mistake the nudge had, on the shutdown gate. An agent
// works on two projects, checkpoints one, keeps working on the other, and the
// host exits: bailing on "this session checkpointed something" meant the second
// project was recorded by neither half of #31 — not by the nudge, which is
// spent and silent at shutdown anyway, and not here.
func TestAProjectWorkedOnAfterCheckpointingAnotherIsStillRecorded(t *testing.T) {
	srv, vault := testServer(t)
	sess := &Session{Server: srv, clientAgent: "cursor"}
	sess.checkpointed("shop")
	fakeTranscripts(t, workedIn("cursor", "billing"))

	sess.recordUnsaved()

	c, err := session.Latest(vault, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("a project worked on after checkpointing a different one was not recorded")
	}
}
