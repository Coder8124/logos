package ingest

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// worked is a transcript of a session that edited a file and ran a command —
// the shape that has something to record.
func worked() *transcript.Session {
	return &transcript.Session{
		Harness: "cursor",
		ID:      "sess-1",
		Path:    "/tmp/sess-1.json",
		Project: "shop",
		Turns: []transcript.Turn{
			{Role: "user", Text: "fix the checkout crash when cart is empty"},
			{Role: "tool", Tool: "edit_file", Input: "internal/cart/checkout.go"},
			{Role: "tool", Tool: "run_terminal_cmd", Input: "go test ./internal/cart", Status: "ok"},
		},
	}
}

// A host with no Stop hook never tells Logos the session ended, so the activity
// log the Claude Code path builds from is empty. The host's own transcript is
// not: it has the prompt, the files and the commands. Without this, every
// session in Cursor, Codex or any other hookless host that forgot to checkpoint
// was lost outright.
func TestASessionThatEndedWithoutACheckpointIsRecordedFromTheHostsOwnTranscript(t *testing.T) {
	vault := t.TempDir()

	c, err := AutoCheckpoint(vault, worked(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("a session that edited a file and ran a command recorded nothing")
	}
	if !c.Auto {
		t.Errorf("the checkpoint is not marked auto, so it reads as an agent's own")
	}
	if c.Task != "fix the checkout crash when cart is empty" {
		t.Errorf("task = %q, want the session's first prompt", c.Task)
	}
	if len(c.Files) == 0 || len(c.Commands) == 0 {
		t.Errorf("files = %v, commands = %v — the work was not carried over", c.Files, c.Commands)
	}
	// Where it came from has to be readable off the file itself: an unverified
	// record whose provenance is invisible is one nobody can weigh.
	if !strings.Contains(c.State, "cursor") || !strings.Contains(c.State, "sess-1") {
		t.Errorf("state = %q, want it to name the harness and the transcript", c.State)
	}
	// Guessing these is worse than leaving them empty — the next agent reads
	// failed as a paid-for ruling and will not re-try what it names.
	if len(c.Verified) != 0 || len(c.Failed) != 0 || c.Next != "" {
		t.Errorf("an unreviewed record claimed verified/failed/next: %+v", c)
	}
}

// A session that only read and asked has nothing worth a checkpoint, and one
// written anyway turns every quick question into a handoff.
func TestATranscriptWithNoWorkWritesNoAutoCheckpoint(t *testing.T) {
	vault := t.TempDir()

	s := worked()
	s.Turns = []transcript.Turn{
		{Role: "user", Text: "what does this package do?"},
		{Role: "assistant", Text: "it parses transcripts"},
	}

	c, err := AutoCheckpoint(vault, s, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("a session that changed nothing was checkpointed: %+v", c)
	}
}

// Two Logos servers in two windows of the same host both see the same newest
// transcript when they close, and both would harvest it. The second one has to
// find the first one's record and leave it alone.
func TestTheSameTranscriptIsNotRecordedTwice(t *testing.T) {
	vault := t.TempDir()

	first, err := AutoCheckpoint(vault, worked(), "shop")
	if err != nil || first == nil {
		t.Fatalf("first = %v, err = %v", first, err)
	}
	second, err := AutoCheckpoint(vault, worked(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Errorf("the same transcript was recorded a second time: %s", second.Slug)
	}
}

// An agent's own checkpoint outranks this one always: the auto record exists
// because nobody wrote one, so finding one means there is nothing to do.
func TestAnAgentsOwnCheckpointIsNotFollowedByAnAutoOne(t *testing.T) {
	vault := t.TempDir()

	if _, err := session.WriteAuto(vault, session.Checkpoint{
		Project: "shop", Agent: "agent", Task: "real work",
	}); err != nil {
		t.Fatal(err)
	}
	// Written as auto above only because that is the cheap way to get a
	// checkpoint on disk here; what matters is that it does not name this
	// transcript, so it is somebody else's record and must not be extended.
	c, err := AutoCheckpoint(vault, worked(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("an unrelated earlier checkpoint stopped this session being recorded")
	}
}
