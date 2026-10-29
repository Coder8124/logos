package contextpack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/session"
)

const (
	oldWaveguide = "re-quoting the waveguide supplier for a lower price — no movement"
	oldTailwind  = "switching the landing page to tailwind — broke the build"
)

// resumeAfterAnOldRuling seeds a project whose relevant dead end sits in a
// checkpoint seven weeks old, behind four recent ones — past both the week
// window and History's reach — and resumes it the way the CLI does.
func resumeAfterAnOldRuling(t *testing.T) (Pack, string) {
	t.Helper()
	ix := seedVault(t)
	now := time.Now()
	cps := []*session.Checkpoint{
		{Agent: "cursor", Task: "price the optics", Failed: []string{oldWaveguide, oldTailwind}, TS: now.Add(-50 * 24 * time.Hour).Unix()},
	}
	for i := 4; i >= 1; i-- {
		cps = append(cps, &session.Checkpoint{Agent: "claude", Task: "lower the waveguide cost",
			State: fmt.Sprintf("pass %d over the optics costs", i),
			Next:  "get a second waveguide supplier quote", TS: now.Add(-time.Duration(i) * time.Hour).Unix()})
	}
	for _, c := range cps {
		c.Project = "kestrel-one"
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	// Commit names a file by the second it ran, and checkpoints are ordered by
	// name, so the old one is given the name it would have had seven weeks ago;
	// otherwise a fast machine names all five in one second and the order is luck.
	dir := filepath.Join(ix.Vault, session.CheckpointDir, "kestrel-one", session.ConvosDir, "_unattached")
	from := filepath.Join(dir, filepath.Base(cps[0].Slug)+".md")
	if err := os.Rename(from, filepath.Join(dir, "20260908-090000-cursor.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	p, err := Build(ix, nil, "", Request{Task: "resume work on kestrel-one", Hint: "kestrel-one", Since: SinceWeek, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return p, p.Render()
}

// A week window and a four-checkpoint history hid the one dead end that was
// about exactly the work being resumed, because it was recorded seven weeks
// ago. The task reaches it, so it comes back; the unrelated one beside it in
// the same checkpoint does not.
func TestResumeSurfacesAnOutOfWindowCheckpointWhenItScoresRelevantToTheTask(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if len(p.Revived) != 1 || p.Revived[0].Text != oldWaveguide {
		t.Fatalf("revived = %+v, want only the waveguide ruling\n%s", p.Revived, out)
	}
	if !strings.Contains(out, "re-quoting the waveguide supplier") {
		t.Errorf("the relevant old ruling is not in the pack:\n%s", out)
	}
	if strings.Contains(out, "tailwind") {
		t.Errorf("an old ruling that shares nothing with the task came back too:\n%s", out)
	}
}

// A ruling from seven weeks ago read as this week's tells an agent the dead end
// is fresh. It carries its age and the words "outside the window", under its
// own heading rather than the current checkpoint's.
func TestAnOutOfWindowCheckpointIsLabelledDistinctlyFromTheNormalWindow(t *testing.T) {
	_, out := resumeAfterAnOldRuling(t)
	head := strings.Index(out, "**Ruled out before this window, and matching this task:**")
	if head < 0 {
		t.Fatalf("no heading of its own for the revived ruling:\n%s", out)
	}
	if !strings.Contains(out[head:], "- (cursor, 1mo ago, outside the window) re-quoting the waveguide") {
		t.Errorf("the revived ruling is not labelled with its author, age and window:\n%s", out)
	}
	if tried := strings.Index(out, "**Already tried, didn't work:**"); tried >= 0 && strings.Index(out[tried:], "waveguide supplier") < head-tried {
		t.Errorf("the old ruling is listed as the latest checkpoint's:\n%s", out)
	}
}

// The window line counted what the filter set aside and then said everything
// fell inside it — beside a ruling that plainly did not. What came back past
// the window is stated apart from what stayed out.
func TestTheSetAsideCountExcludesItemsSurfacedByRelevance(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if p.OutOfWindow != 0 {
		t.Errorf("OutOfWindow = %d, want 0: nothing the window holds was set aside", p.OutOfWindow)
	}
	if strings.Contains(out, "Everything recorded falls inside it") {
		t.Errorf("the window line says nothing is outside it, above a ruling from outside it:\n%s", out)
	}
	if strings.Contains(out, "set aside") {
		t.Errorf("the revived ruling is counted as set aside:\n%s", out)
	}
	if !strings.Contains(out, "1 older ruled-out approach from outside it is listed because it matches this task") {
		t.Errorf("the window line does not account for the revived ruling:\n%s", out)
	}
}

// The SessionStart hook resumes with LOGOS_EMBED=off, so relevance there is
// keyword matching. A reader who assumes a model looked trusts a miss the
// keywords cannot back, so the pack says how it matched.
func TestWithEmbeddingsOffResumeSaysRelevanceIsLexicalOrOff(t *testing.T) {
	p, out := resumeAfterAnOldRuling(t)
	if !p.ReviveLexical {
		t.Error("a pack built with no embedding model did not record that relevance was lexical")
	}
	if !strings.Contains(out, "by keyword only (no embedding model)") {
		t.Errorf("the pack does not say relevance was keyword-only:\n%s", out)
	}
}

// Build filters the checkpoint it hands over to this repository, and the cost
// count read the project's history again without that filter, so another
// repository under the same name counted as a session that hit this one's
// dead end.
func TestAnEarlierSessionInAnotherRepositoryIsNotCountedAsHittingTheDeadEnd(t *testing.T) {
	work, personal := gitRepo(t), gitRepo(t)
	ix := seedVault(t)
	now := time.Now()
	ruling := "caching the cart — the cache evicts on every write, so it never hits"
	for _, c := range []struct {
		dir, host string
		age       time.Duration
	}{{personal, "sess-personal", 48 * time.Hour}, {work, "sess-work", time.Hour}} {
		remote, root := gitstate.Identity(c.dir)
		if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
			Project: "shop", Agent: "claude", HostSession: c.host, Task: "speed up checkout",
			Failed: []string{ruling}, TS: now.Add(-c.age).Unix(),
			Git: gitstate.State{Branch: "main", Commit: "abc1234", Remote: remote, Root: root},
		}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "shop", Dir: work, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint == nil || p.Checkpoint.HostSession != "sess-work" {
		t.Fatalf("checkpoint = %+v, want this repository's", p.Checkpoint)
	}
	if p.Cost != nil {
		t.Errorf("another repository's session was counted as hitting this dead end: %+v\n%s", *p.Cost, p.Render())
	}
}
