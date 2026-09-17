package contextpack

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/session"
)

// ~/work/api and ~/personal/api are both "api". A session opened in the
// personal one was told to carry on rotating a client's production keys,
// because the name was all that tied a checkpoint to the work.
func TestACheckpointFromAnotherRepositoryWithTheSameNameIsNotHandedOver(t *testing.T) {
	work, personal := gitRepo(t), gitRepo(t)
	ix := seedVault(t)
	now := time.Now()
	remote, root := gitstate.Identity(work)
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "rotate the payments API keys for ACME corp",
		Next: "revoke old key in prod", TS: now.Add(-time.Minute).Unix(),
		Git: gitstate.State{Branch: "main", Commit: "abc1234", Remote: remote, Root: root},
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: personal, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Empty() {
		t.Error("the pack is empty, so resume prints \"nothing recorded\" and never says why")
	}
	out := p.Render()
	if strings.Contains(out, "ACME") || strings.Contains(out, "revoke old key") {
		t.Errorf("the other repository's handoff was given to this one:\n%s", out)
	}
	if !strings.Contains(out, "came from another repository") || !strings.Contains(out, ".logos-project") {
		t.Errorf("nothing says why there is no handoff:\n%s", out)
	}

	// The repository that wrote it still gets it back.
	p, err = Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: work, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); !strings.Contains(out, "revoke old key in prod") {
		t.Errorf("the repository lost its own handoff:\n%s", out)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	return dir
}

// "10 files uncommitted" told the next agent how many and not which, though
// the checkpoint had recorded the paths all along.
func TestThePackNamesTheUncommittedFilesTheCheckpointRecorded(t *testing.T) {
	repo := gitRepo(t)
	ix := seedVault(t)
	now := time.Now()
	remote, root := gitstate.Identity(repo)
	files := []string{"internal/setup/plugin.go", "internal/health/health.go"}
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "wire up the plugin check",
		Next: "run the gate", TS: now.Add(-time.Minute).Unix(),
		Git: gitstate.State{
			Branch: "main", Commit: "abc1234", Remote: remote, Root: root,
			Dirty: 47, Files: files,
		},
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	for _, f := range files {
		if !strings.Contains(out, f) {
			t.Errorf("the pack says how many files were uncommitted but not that %s was one:\n%s", f, out)
		}
	}
	if !strings.Contains(out, "2 of 47") {
		t.Errorf("a capped list reads as the whole list; it should say 2 of 47:\n%s", out)
	}
}
