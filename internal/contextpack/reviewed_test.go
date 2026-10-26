package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// A drift warning used to come back on every resume with nothing that could
// answer it. Resume now names the command that does, and honours its answers:
// a retired ruling is left out (and counted, so the handoff is not just
// shorter), a re-confirmed one is measured from the commit it was confirmed at.
func TestResumeHonoursWhatReviewStaleDecided(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	_, anchor := gitstate.Head(repo)
	remote, root := gitstate.Identity(repo)
	ix := seedVault(t)
	now := time.Now()
	retired := "streaming the export — internal/parse/reader.go loads the whole file"
	confirmed := "mmap the export — internal/parse/reader.go needs a seekable source"
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "import the export", TS: now.Add(-time.Hour).Unix(),
		Git:    gitstate.State{Branch: "main", Commit: anchor, Remote: remote, Root: root},
		Failed: []string{retired, confirmed}, Next: "split the export",
	}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")
	_, head := gitstate.Head(repo)

	build := func() string {
		t.Helper()
		p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		return p.Render()
	}
	if out := build(); !strings.Contains(out, "2 rulings may be out of date — `logos review --stale`") {
		t.Fatalf("resume does not name the command that answers its drift warnings:\n%s", out)
	}

	if err := session.Retire(ix.Vault, "api", retired); err != nil {
		t.Fatal(err)
	}
	// Re-wrapped and re-cased: the same ruling, as a later checkpoint might
	// restate it.
	if err := session.Reconfirm(ix.Vault, "api", strings.ToUpper(confirmed[:4])+confirmed[4:]+"\n", head); err != nil {
		t.Fatal(err)
	}
	out := build()
	if strings.Contains(out, "loads the whole file") {
		t.Errorf("a retired ruling is still handed over:\n%s", out)
	}
	if !strings.Contains(out, "1 ruling retired with `logos review --stale` left out") {
		t.Errorf("the retired ruling vanished without a word:\n%s", out)
	}
	if !strings.Contains(out, "needs a seekable source") {
		t.Fatalf("a re-confirmed ruling was dropped:\n%s", out)
	}
	if strings.Contains(out, "may no longer hold") || strings.Contains(out, "may be out of date") {
		t.Errorf("a ruling re-confirmed at head is still warned about:\n%s", out)
	}
}

// Each of these is a warning resume gave that `logos review --stale` could not
// settle, or a ruling it dropped that it should have kept.

// A ruling's structured anchor says what it depends on; the text naming
// another file must not make resume warn when --stale, which reads the
// anchor, offers nothing.
func TestResumeMeasuresARulingByItsAnchorLikeReviewStaleDoes(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	commitFile(t, repo, "internal/parse/sink.go", "sink\n")
	_, anchor := gitstate.Head(repo)
	remote, root := gitstate.Identity(repo)
	ix := seedVault(t)
	now := time.Now()
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "import", TS: now.Add(-time.Hour).Unix(),
		Git:    gitstate.State{Branch: "main", Commit: anchor, Remote: remote, Root: root},
		Failed: []string{"route: stream through internal/parse/reader.go | observation: the sink buffers it all | anchor: git:internal/parse/sink.go"},
		Next:   "split",
	}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); strings.Contains(out, "may be out of date") || strings.Contains(out, "may no longer hold") {
		t.Errorf("warned about a file the ruling's anchor does not depend on:\n%s", out)
	}
}

// Restated after the file changed, a ruling is measured from the restatement;
// the older copy, from before the change, must not warn on its behalf.
func TestARulingRestatedAfterItsFileChangedIsNotWarnedAbout(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	_, first := gitstate.Head(repo)
	remote, root := gitstate.Identity(repo)
	ix := seedVault(t)
	now := time.Now()
	ruling := "streaming the export — internal/parse/reader.go loads the whole file"
	cp := func(ts time.Time, commit string) {
		t.Helper()
		if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
			Project: "api", Agent: "claude", Task: "import", TS: ts.Unix(),
			Git:    gitstate.State{Branch: "main", Commit: commit, Remote: remote, Root: root},
			Failed: []string{ruling}, Next: "split",
		}); err != nil {
			t.Fatal(err)
		}
	}
	cp(now.Add(-2*time.Hour), first)
	commitFile(t, repo, "internal/parse/reader.go", "still loads all\n")
	_, head := gitstate.Head(repo)
	cp(now.Add(-time.Hour), head)
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); strings.Contains(out, "may be out of date") || strings.Contains(out, "may no longer hold") {
		t.Errorf("the older copy of a restated ruling still warns:\n%s", out)
	}
}

// A retire covers what was recorded up to it. The same dead end recorded again
// afterwards is a new finding, and dropping it on a text match would send the
// next agent back into it.
func TestADeadEndRecordedAgainAfterItWasRetiredIsHandedOver(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	ruling := "streaming the export — internal/parse/reader.go loads the whole file"
	cp := func(ts time.Time) {
		t.Helper()
		if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
			Project: "api", Agent: "claude", Task: "import", TS: ts.Unix(),
			Failed: []string{ruling}, Next: "split",
		}); err != nil {
			t.Fatal(err)
		}
	}
	cp(now.Add(-time.Hour))
	if err := session.Retire(ix.Vault, "api", ruling); err != nil {
		t.Fatal(err)
	}
	cp(now.Add(2 * time.Minute))
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Now: now.Add(3 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "loads the whole file") {
		t.Errorf("a dead end recorded again after its retire was dropped:\n%s", out)
	}
	if strings.Contains(out, "retired with") {
		t.Errorf("a ruling still handed over is counted as left out:\n%s", out)
	}
}

// --stale offers memories too, so a drifted one in the pack gets the hint.
func TestResumesStaleHintCountsDriftedMemories(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	_, anchor := gitstate.Head(repo)
	ix := seedVault(t)
	m := memory.Memory{
		Text: "internal/parse/reader.go loads the whole file, because the sink assumes it is all there",
		Kind: memory.Fact, Source: "manual", Project: "api", Commit: anchor,
	}
	rc, err := memory.Store(ix.DB, nil, "", &m)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Pin(ix.DB, rc.ID); err != nil {
		t.Fatal(err)
	}
	remote, root := gitstate.Identity(repo)
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "import", TS: time.Now().Add(-time.Hour).Unix(),
		Git:  gitstate.State{Branch: "main", Commit: anchor, Remote: remote, Root: root},
		Next: "split",
	}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); !strings.Contains(out, "1 memory may be out of date — `logos review --stale`") {
		t.Errorf("a drifted memory got no hint:\n%s", out)
	}
}
