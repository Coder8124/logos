package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
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
