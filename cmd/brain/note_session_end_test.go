package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/session"
)

// uncommittedOn reads the working notes the next handoff will list under
// "Recorded since, not yet checkpointed".
func uncommittedOn(t *testing.T, project string) []session.Note {
	t.Helper()
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	notes, err := session.Uncommitted(ix.DB, project)
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

// The Claude Code SessionEnd hook notes every session end. After a session
// that checkpointed, that note opened a fresh "not yet checkpointed" section
// under a perfectly good handoff, so the handoff looked stale.
func TestASessionEndNoteAfterACheckpointIsNotWritten(t *testing.T) {
	standIn(t, "elsewhere")
	if err := runCheckpoint([]string{"widgets", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	t.Setenv("BRAIN_NOTE_IF_UNCOMMITTED", "1") // as the SessionEnd hook runs it

	out := captureStdout(t, func() {
		if err := runNote([]string{"widgets", "claude-code session ended"}); err != nil {
			t.Fatalf("note: %v", err)
		}
	})
	if notes := uncommittedOn(t, "widgets"); len(notes) != 0 {
		t.Errorf("a checkpointed session still gained %d uncommitted note(s): %+v", len(notes), notes)
	}
	if !strings.HasPrefix(out, "skipped") {
		t.Errorf("a skipped note must say it was skipped, got %q", out)
	}
}

// Four sessions that never checkpointed used to leave four identical lines.
// The first marks that the work was left open; the rest add nothing.
func TestRepeatedSessionEndNotesCollapseIntoOne(t *testing.T) {
	standIn(t, "elsewhere")
	if err := runNote([]string{"widgets", "rewired the cache"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	t.Setenv("BRAIN_NOTE_IF_UNCOMMITTED", "1") // as the SessionEnd hook runs it
	for i := 0; i < 4; i++ {
		captureStdout(t, func() {
			if err := runNote([]string{"widgets", "claude-code session ended"}); err != nil {
				t.Fatalf("note: %v", err)
			}
		})
	}

	var ended int
	for _, n := range uncommittedOn(t, "widgets") {
		if n.Text == "claude-code session ended" {
			ended++
		}
	}
	if ended != 1 {
		t.Errorf("want 1 session-ended note after four sessions, got %d", ended)
	}
}
