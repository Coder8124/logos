package mcpserver

import (
	"strings"
	"testing"
)

// noted stands in for the session having called note_progress n times without
// checkpointing since.
func noted(n int) *Session {
	s := &Session{}
	for i := 0; i < n; i++ {
		s.notedProgress()
	}
	return s
}

// The floor under #31. A host with no hooks never tells Logos a session ended,
// and stderr goes somewhere nobody reads, but the model reads every tool result
// it gets — so the result itself is the one channel that reaches it in every
// host there is.
func TestASessionCarryingUnsavedProgressIsToldSoInAToolResult(t *testing.T) {
	s := noted(notesBeforeNudge)

	nudge := s.unsavedNudge()

	if nudge == "" {
		t.Fatal("a session with unsaved progress and no checkpoint was told nothing")
	}
	if !strings.Contains(nudge, "checkpoint") {
		t.Errorf("nudge = %q, want it to name what to do about it", nudge)
	}
}

// One line, not one per call. A sentence repeated on every result is noise the
// model learns to skip, and it would push real answer text out of the window.
func TestTheNudgeIsSaidOnceAndNotOnEveryToolCallAfterwards(t *testing.T) {
	s := noted(notesBeforeNudge)

	if s.unsavedNudge() == "" {
		t.Fatal("the first result carried nothing")
	}
	for i := 0; i < 3; i++ {
		s.notedProgress()
		if again := s.unsavedNudge(); again != "" {
			t.Fatalf("the nudge was repeated on a later result: %q", again)
		}
	}
}

// A single note is already answered by note_progress's own receipt, which says
// it is uncommitted until you checkpoint. Nudging there too teaches the model
// that the line means nothing.
func TestOneNoteIsNotEnoughToNudge(t *testing.T) {
	if nudge := noted(1).unsavedNudge(); nudge != "" {
		t.Errorf("a session one note in was nudged: %q", nudge)
	}
}

// A session that has checkpointed has nothing unsaved to warn about, however
// much it has noted since — the checkpoint carried the notes in.
func TestASessionThatHasCheckpointedIsNotNudged(t *testing.T) {
	s := noted(notesBeforeNudge)
	s.checkpointed()

	if nudge := s.unsavedNudge(); nudge != "" {
		t.Errorf("a session that saved its work was told it had not: %q", nudge)
	}
}

// A long session checkpoints in the middle and keeps working. The progress
// built up after that is as unsaved as the first batch was, so the nudge has to
// come back rather than being spent for good.
func TestProgressRecordedAfterACheckpointNudgesAgain(t *testing.T) {
	s := noted(notesBeforeNudge)
	s.unsavedNudge()
	s.checkpointed()

	for i := 0; i < notesBeforeNudge; i++ {
		s.notedProgress()
	}

	if s.unsavedNudge() == "" {
		t.Error("work done after a checkpoint was never flagged as unsaved")
	}
}
