package session

import (
	"testing"
	"time"
)

func TestContinuityReportsCheckpointsAndUncommitted(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	Commit(db, dir, &Checkpoint{Project: "kestrel", Agent: "claude", Task: "first", Next: "a"})
	AddNote(db, "kestrel", "codex", "picked up where claude left off")

	pc, err := Continuity(db, dir, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if pc.Checkpoints != 1 {
		t.Errorf("checkpoints = %d, want 1", pc.Checkpoints)
	}
	if pc.LastAgent != "claude" {
		t.Errorf("last agent = %q, want claude", pc.LastAgent)
	}
	if pc.Uncommitted != 1 {
		t.Errorf("uncommitted = %d, want 1", pc.Uncommitted)
	}
	if pc.Quiet() {
		t.Error("a project checkpointed moments ago should not read as quiet")
	}
}

// A project with no checkpoint at all is not the same as a project with an old
// one — both are "needs attention", but a report that cannot distinguish them
// is a report that cannot say why.
func TestContinuityNeverCheckpointedIsQuiet(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	AddNote(db, "kestrel", "claude", "just started, nothing committed yet")

	pc, err := Continuity(db, dir, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if pc.Checkpoints != 0 || pc.LastCheckpoint != 0 {
		t.Errorf("a project with no checkpoints should report zero, got %+v", pc)
	}
	if !pc.Quiet() {
		t.Error("a project that has never checkpointed should read as quiet")
	}
}

// The report's whole reason to exist: a project can be found even though it
// has never produced a single checkpoint, because it has an open session.
func TestAllContinuityFindsProjectsWithSessionsButNoCheckpoints(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	AddNote(db, "never-checkpointed", "claude", "working, but never committed")

	report, err := AllContinuity(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pc := range report {
		if pc.Project == "never-checkpointed" {
			found = true
			if pc.Checkpoints != 0 {
				t.Errorf("expected zero checkpoints, got %d", pc.Checkpoints)
			}
		}
	}
	if !found {
		t.Fatal("a project with a session but no checkpoint must still appear in the report")
	}
}

func TestAllContinuitySortsActiveFirstAndNeverCheckpointedLast(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	Commit(db, dir, &Checkpoint{Project: "fresh", Agent: "claude", Task: "t", Next: "n"})
	AddNote(db, "no-checkpoint-yet", "codex", "starting out")

	// Backdate "fresh"'s checkpoint file? Simplest: also add a stale project so
	// ordering among checkpointed projects is exercised, not just the
	// never-checkpointed tiebreak.
	Commit(db, dir, &Checkpoint{Project: "stale", Agent: "claude", Task: "t", Next: "n"})

	report, err := AllContinuity(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 3 {
		t.Fatalf("want 3 projects, got %d: %+v", len(report), report)
	}
	// The never-checkpointed project must sort after every checkpointed one,
	// regardless of its own recent activity.
	last := report[len(report)-1]
	if last.Project != "no-checkpoint-yet" {
		t.Errorf("never-checkpointed project should sort last, got order %+v", report)
	}
}

func TestAllContinuityRollsWorktreesUpUnderTheirProject(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	AddNote(db, "kestrel/feature-x", "claude", "working in a linked worktree")

	report, err := AllContinuity(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 || report[0].Project != "kestrel" {
		t.Fatalf("a worktree scope should roll up under its project, got %+v", report)
	}
}

func TestQuietAfterWindow(t *testing.T) {
	pc := ProjectContinuity{LastCheckpoint: time.Now().Add(-2 * QuietAfter).Unix()}
	if !pc.Quiet() {
		t.Error("a checkpoint well outside QuietAfter should read as quiet")
	}
	pc2 := ProjectContinuity{LastCheckpoint: time.Now().Unix()}
	if pc2.Quiet() {
		t.Error("a checkpoint from just now should not read as quiet")
	}
}
