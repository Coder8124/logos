package deadend

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/session"
)

// The tests that matter here are the ones about restraint.
//
// A checker that fires on everything is worse than no checker: the agent learns
// to skim past it, and the same knowledge is now sitting behind a reason to
// ignore it. So most of what follows is about what must *not* match.

func seed(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}

	commit := func(project, agent string, failed []string) {
		if err := session.Commit(ix.DB, dir, &session.Checkpoint{
			Project: project, Agent: agent, Task: "work", Failed: failed,
			Next: "carry on",
		}); err != nil {
			t.Fatal(err)
		}
	}
	commit("kestrel-one", "claude", []string{
		"Switching to a plastic frame — fails the drop test at 1.2m",
		"Re-quoting the waveguide with Lumus — no movement under 10k units",
	})
	commit("website", "cursor", []string{
		"Contentful — the pricing tier jumps at exactly our seat count",
	})
	return dir, ix.DB
}

// The point of the whole package: the approach an agent is about to suggest was
// ruled out by someone else, on a different project, and nothing would have
// surfaced it because nobody thought to search for it.
func TestFindsARulingWordedTheSameWay(t *testing.T) {
	dir, db := seed(t)

	hits, err := Check(dir, db, nil, "", "switch to a plastic frame to save weight", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the plastic frame was ruled out and should have been found")
	}
	if !strings.Contains(hits[0].Text, "plastic frame") {
		t.Errorf("wrong ruling led: %q", hits[0].Text)
	}
	if hits[0].Agent != "claude" {
		t.Errorf("who ruled it out is part of the finding, got %q", hits[0].Agent)
	}
	if hits[0].Elsewhere {
		t.Error("this was ruled out on the project being asked about")
	}
}

// Cross-project transfer is the valuable case and the risky one. It must be
// found, and it must be marked, because a constraint that killed an approach
// on other hardware may not apply here.
func TestRulingsFromOtherProjectsAreFlagged(t *testing.T) {
	dir, db := seed(t)

	hits, err := Check(dir, db, nil, "", "use Contentful for the pricing pages", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a ruling from another project should still be found")
	}
	if !hits[0].Elsewhere {
		t.Error("a ruling from another project must be marked as such")
	}
	if out := Render("use Contentful", hits); !strings.Contains(out, "may not transfer") {
		t.Errorf("the render must caveat a cross-project ruling:\n%s", out)
	}
}

// Restraint. An unrelated proposal must come back clean, or the tool becomes
// noise the model learns to skip.
func TestUnrelatedApproachesDoNotMatch(t *testing.T) {
	dir, db := seed(t)

	for _, proposal := range []string{
		"add a dark mode to the settings screen",
		"write the release notes for this build",
		"upgrade the CI runner image",
	} {
		hits, err := Check(dir, db, nil, "", proposal, "kestrel-one", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) > 0 {
			t.Errorf("%q matched %q — a false interruption is worse than a miss",
				proposal, hits[0].Text)
		}
	}
}

// Nothing recorded must read as "not a repeat", not as approval. The
// distinction is the difference between a memory that knows its limits and one
// that endorses whatever it has not heard of.
func TestSilenceIsNotEndorsement(t *testing.T) {
	dir, db := seed(t)

	hits, _ := Check(dir, db, nil, "", "rewrite the firmware in Rust", "kestrel-one", 5)
	out := Render("rewrite the firmware in Rust", hits)
	if !strings.Contains(out, "No record") {
		t.Errorf("want an explicit no-record answer:\n%s", out)
	}
	if !strings.Contains(out, "not the same as it being a good idea") {
		t.Errorf("absence of a ruling must not read as approval:\n%s", out)
	}
}

// An agent killed before it could check in leaves its findings only in working
// notes. Those are exactly the dead ends most likely to be repeated.
func TestUncommittedFailuresCount(t *testing.T) {
	dir, db := seed(t)
	if _, err := session.AddNote(db, "kestrel-one", "claude",
		"Tried dropping the second microphone; the audio team vetoed it, beamforming needs two"); err != nil {
		t.Fatal(err)
	}

	hits, err := Check(dir, db, nil, "", "drop the second microphone to cut cost", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a failure recorded in a working note should still be found")
	}
	if hits[0].Source != FromNote {
		t.Errorf("want it marked as an uncommitted note, got %q", hits[0].Source)
	}
	if out := Render("drop the second mic", hits); !strings.Contains(out, "never checkpointed") {
		t.Errorf("the render should say the finding was never committed:\n%s", out)
	}
}

// A working note that merely reports progress is not a dead end. Without an
// explicit failure marker, ordinary notes would flood the corpus and every
// check would fire.
func TestOrdinaryNotesAreNotDeadEnds(t *testing.T) {
	dir, db := seed(t)
	if _, err := session.AddNote(db, "kestrel-one", "claude",
		"Reviewed the plastic frame supplier quotes this morning"); err != nil {
		t.Fatal(err)
	}

	all, err := Collect(dir, db, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if strings.Contains(r.Text, "Reviewed the plastic frame supplier quotes") {
			t.Error("a progress note was collected as a dead end")
		}
	}
}

// A recorded failure is evidence, not a veto. The render must leave room to
// proceed, or it will stop work that should happen.
func TestTheInterruptionDoesNotForbid(t *testing.T) {
	dir, db := seed(t)
	hits, _ := Check(dir, db, nil, "", "switch to a plastic frame", "kestrel-one", 5)

	out := Render("switch to a plastic frame", hits)
	if !strings.Contains(out, "evidence, not a verdict") {
		t.Errorf("the interruption must leave room to disagree:\n%s", out)
	}
	if !strings.Contains(out, "say that it has been tried") {
		t.Errorf("the agent should be told to surface this to the user:\n%s", out)
	}
}

// The whole vault is searched, not just one project — that is what makes this
// different from reading the current project's checkpoint.
func TestCollectSpansEveryProject(t *testing.T) {
	dir, db := seed(t)

	all, err := Collect(dir, db, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.Project] = true
	}
	if !seen["kestrel-one"] || !seen["website"] {
		t.Errorf("want dead ends from every project, got %v", seen)
	}
}
