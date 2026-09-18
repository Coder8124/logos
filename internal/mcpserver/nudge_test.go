package mcpserver

import (
	"strings"
	"testing"
)

// noted stands in for the session having called note_progress n times on
// project without checkpointing it since.
func noted(project string, n int) *Session {
	s := &Session{}
	for i := 0; i < n; i++ {
		s.notedProgress(project)
	}
	return s
}

// said is the nudge as the host would see it: composed into a result, and that
// result delivered.
func said(s *Session) string {
	out := s.unsavedNudge()
	s.nudgeSent(true)
	return out
}

// The floor under #31. A host with no hooks never tells Logos a session ended,
// and stderr goes somewhere nobody reads, but the model reads every tool result
// it gets — so the result itself is the one channel that reaches it in every
// host there is.
func TestASessionCarryingUnsavedProgressIsToldSoInAToolResult(t *testing.T) {
	s := noted("shop", notesBeforeNudge)

	nudge := said(s)

	if nudge == "" {
		t.Fatal("a session with unsaved progress and no checkpoint was told nothing")
	}
	if !strings.Contains(nudge, "checkpoint") {
		t.Errorf("nudge = %q, want it to name what to do about it", nudge)
	}
	if !strings.Contains(nudge, "shop") {
		t.Errorf("nudge = %q, want it to name the project that is unsaved", nudge)
	}
}

// One line, not one per call. A sentence repeated on every result is noise the
// model learns to skip, and it would push real answer text out of the window.
func TestTheNudgeIsSaidOnceAndNotOnEveryToolCallAfterwards(t *testing.T) {
	s := noted("shop", notesBeforeNudge)

	if said(s) == "" {
		t.Fatal("the first result carried nothing")
	}
	for i := 0; i < 3; i++ {
		s.notedProgress("shop")
		if again := said(s); again != "" {
			t.Fatalf("the nudge was repeated on a later result: %q", again)
		}
	}
}

// A host that cancels a slow call has its reply dropped unread. Spending the
// session's one warning on a result nobody saw is the exact failure the nudge
// exists to prevent, so a dropped response leaves it armed.
func TestANudgeOnAResultTheHostNeverReceivedIsSaidAgain(t *testing.T) {
	s := noted("shop", notesBeforeNudge)

	if s.unsavedNudge() == "" {
		t.Fatal("nothing was composed to be dropped")
	}
	s.nudgeSent(false)

	if again := said(s); again == "" {
		t.Error("the warning was spent on a response the host never got")
	}
}

// A single note is already answered by note_progress's own receipt, which says
// it is uncommitted until you checkpoint. Nudging there too teaches the model
// that the line means nothing.
func TestOneNoteIsNotEnoughToNudge(t *testing.T) {
	if nudge := said(noted("shop", 1)); nudge != "" {
		t.Errorf("a session one note in was nudged: %q", nudge)
	}
}

// A project that has been checkpointed has nothing unsaved to warn about,
// however much was noted before — the checkpoint carried the notes in.
func TestAProjectThatHasBeenCheckpointedIsNotNudged(t *testing.T) {
	s := noted("shop", notesBeforeNudge)
	s.checkpointed("shop")

	if nudge := said(s); nudge != "" {
		t.Errorf("a project whose work was saved was said to be unsaved: %q", nudge)
	}
}

// Notes and checkpoints are both per project. A session-wide count would let a
// checkpoint on one project silence unsaved work on another — and in a hookless
// host the transcript path skips the session too once anything is checkpointed,
// so that project's work would go unrecorded by either half of #31.
func TestCheckpointingOneProjectDoesNotSilenceUnsavedWorkOnAnother(t *testing.T) {
	s := noted("shop", notesBeforeNudge)
	s.checkpointed("billing")

	nudge := said(s)

	if nudge == "" {
		t.Fatal("a checkpoint on an unrelated project silenced the warning")
	}
	if !strings.Contains(nudge, "shop") {
		t.Errorf("nudge = %q, want it to name shop", nudge)
	}
}

// The mirror of the same mistake: one note each on three projects is three
// sessions' worth of nothing, not one session with work to save.
func TestOneNoteOnEachOfSeveralProjectsIsNotAggregatedIntoANudge(t *testing.T) {
	s := &Session{}
	for _, p := range []string{"shop", "billing", "search"} {
		s.notedProgress(p)
	}

	if nudge := said(s); nudge != "" {
		t.Errorf("notes spread across projects were counted as one project's: %q", nudge)
	}
}

// A long session checkpoints in the middle and keeps working. The progress
// built up after that is as unsaved as the first batch was, so the nudge has to
// come back rather than being spent for good.
func TestProgressRecordedAfterACheckpointNudgesAgain(t *testing.T) {
	s := noted("shop", notesBeforeNudge)
	said(s)
	s.checkpointed("shop")

	for i := 0; i < notesBeforeNudge; i++ {
		s.notedProgress("shop")
	}

	if said(s) == "" {
		t.Error("work done after a checkpoint was never flagged as unsaved")
	}
}
