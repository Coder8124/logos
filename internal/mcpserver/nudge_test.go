package mcpserver

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"

	_ "modernc.org/sqlite"
)

// noted stands in for the session having called note_progress n times on
// project without checkpointing it since.
func noted(project string, n int) *Session {
	s := &Session{}
	for i := 0; i < n; i++ {
		s.recordedWork(project)
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
	s := noted("shop", workBeforeNudge)

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
	s := noted("shop", workBeforeNudge)

	if said(s) == "" {
		t.Fatal("the first result carried nothing")
	}
	for i := 0; i < 3; i++ {
		s.recordedWork("shop")
		if again := said(s); again != "" {
			t.Fatalf("the nudge was repeated on a later result: %q", again)
		}
	}
}

// A host that cancels a slow call has its reply dropped unread. Spending the
// session's one warning on a result nobody saw is the exact failure the nudge
// exists to prevent, so a dropped response leaves it armed.
func TestANudgeOnAResultTheHostNeverReceivedIsSaidAgain(t *testing.T) {
	s := noted("shop", workBeforeNudge)

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
	s := noted("shop", workBeforeNudge)
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
	s := noted("shop", workBeforeNudge)
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
		s.recordedWork(p)
	}

	if nudge := said(s); nudge != "" {
		t.Errorf("notes spread across projects were counted as one project's: %q", nudge)
	}
}

// A long session checkpoints in the middle and keeps working. The progress
// built up after that is as unsaved as the first batch was, so the nudge has to
// come back rather than being spent for good.
func TestProgressRecordedAfterACheckpointNudgesAgain(t *testing.T) {
	s := noted("shop", workBeforeNudge)
	said(s)
	s.checkpointed("shop")

	for i := 0; i < workBeforeNudge; i++ {
		s.recordedWork("shop")
	}

	if said(s) == "" {
		t.Error("work done after a checkpoint was never flagged as unsaved")
	}
}

// A checkpoint files itself under the project's safe scope — Commit lowercases
// and dash-collapses the name — while the notes were counted under whatever the
// agent typed. Keyed on the raw spelling, a checkpoint on FleetBuilder never
// cleared FleetBuilder's notes: the agent was told its work was unsaved on the
// very call that saved it, and the warning was spent, so the next genuine
// stretch of unsaved work on that project went unwarned.
func TestAProjectIsCountedAndClearedUnderTheSameNameHoweverItWasSpelled(t *testing.T) {
	s := noted("FleetBuilder", workBeforeNudge)

	s.checkpointed("fleetbuilder") // as session.Commit files it

	if nudge := said(s); nudge != "" {
		t.Errorf("a checkpointed project was still said to be unsaved: %q", nudge)
	}
}

// The same mistake seen from the other side: one project the agent spelled
// inconsistently is one project's worth of unsaved work, not three sessions of
// too little to mention. Only spellings that file to the same scope count as
// the same project — "Fleet Builder" is a different scope by the same rule that
// gives it a different checkpoint path, and is meant to be.
func TestSpellingsOfOneProjectThatFileTogetherAreNotCountedAsSeparateProjects(t *testing.T) {
	s := &Session{}
	s.recordedWork("FleetBuilder")
	s.recordedWork("fleetbuilder")
	s.recordedWork("  FleetBuilder  ")

	if said(s) == "" {
		t.Error("three notes on one project were counted as separate projects with one note each")
	}
}

// workingSession is a session over a real scratch vault and index, so a call
// goes through dispatch the way a host's does. The blind-spot tests below are
// about what the dispatcher counts, which a Session built by hand cannot show.
func workingSession(t *testing.T) *Session {
	t.Helper()
	// Worktree scoping off: these tests name their project and care about what
	// counts as work, not about where continuity is filed.
	t.Setenv("LOGOS_WORKTREE", "")
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := memory.Init(db); err != nil {
		t.Fatal(err)
	}
	return &Session{Server: New(db, nil, dir), clientAgent: "cursor"}
}

// The nudge's blind spot, and the reason it was worth fixing: its only signal
// was note_progress, so the agent that records nothing as it goes — precisely
// the one whose session is lost if it ends without a checkpoint — was the one
// agent it could never warn. A memory written is work happening here just as
// much as a note is.
func TestAnAgentThatWorksWithoutEverCallingNoteProgressIsStillWarned(t *testing.T) {
	s := workingSession(t)

	for i := 0; i < workBeforeNudge; i++ {
		if _, err := s.dispatch("remember", map[string]any{
			"text":    fmt.Sprintf("the cart total is computed in cents, rule %d", i),
			"project": "shop",
		}); err != nil {
			t.Fatal(err)
		}
	}

	nudge := said(s)

	if nudge == "" {
		t.Fatal("a session that recorded work on shop and never checkpointed was told nothing")
	}
	if !strings.Contains(nudge, "shop") {
		t.Errorf("nudge = %q, want it to name the project", nudge)
	}
}

// Asking before_you_try is the agent about to change something, which is the
// moment the session starts being worth saving.
func TestCheckingAnApproachCountsAsWorkOnTheProject(t *testing.T) {
	s := workingSession(t)

	for i := 0; i < workBeforeNudge; i++ {
		if _, err := s.dispatch("before_you_try", map[string]any{
			"approach": fmt.Sprintf("rewrite the checkout in approach %d", i),
			"project":  "shop",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if said(s) == "" {
		t.Error("a session that checked three approaches and never checkpointed was told nothing")
	}
}

// The other half of the same judgement: reading the vault is not working in it.
// A session that resumes a project, recalls a few things and reads why a file
// is the way it is has produced nothing to save, and warning it there teaches
// the model that the line means nothing.
func TestASessionThatOnlyReadsTheVaultIsNotWarned(t *testing.T) {
	s := workingSession(t)

	for i := 0; i < workBeforeNudge+2; i++ {
		if _, err := s.dispatch("recall", map[string]any{"query": "checkout", "project": "shop"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.dispatch("resume", map[string]any{"project": "shop"}); err != nil {
		t.Fatal(err)
	}

	if nudge := said(s); nudge != "" {
		t.Errorf("a session that only read the vault was told it had unsaved work: %q", nudge)
	}
}
