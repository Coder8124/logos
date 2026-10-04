package contextpack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/session"
)

func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "--", name},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "change " + name},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %s: %v %s", args[0], err, out)
		}
	}
}

// "Streaming fails because reader.go loads the whole file" was handed to the
// next agent as settled three weeks after reader.go was rewritten to read in
// chunks — the fix the ruling ruled out was now the one that worked. The
// ruling is kept, and says the file it blames has moved.
func TestARuledOutApproachWhoseFileChangedSinceIsFlaggedAtResume(t *testing.T) {
	repo := gitRepo(t)
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	commitFile(t, repo, "internal/sink/writer.go", "write\n")
	_, anchor := gitstate.Head(repo)
	remote, root := gitstate.Identity(repo)
	ix := seedVault(t)
	now := time.Now()
	git := gitstate.State{Branch: "main", Commit: anchor, Remote: remote, Root: root}
	for _, c := range []session.Checkpoint{{
		Project: "api", Agent: "claude", Task: "import the export", TS: now.Add(-2 * time.Hour).Unix(), Git: git,
		Failed: []string{"buffering the sink — internal/sink/writer.go flushes per row, so it is no faster"},
	}, {
		Project: "api", Agent: "claude", Task: "import the export", TS: now.Add(-time.Hour).Unix(), Git: git,
		Failed: []string{"streaming the export — internal/parse/reader.go loads the whole file, out of memory at 2GB"},
		Next:   "split the export",
	}} {
		if err := session.Commit(ix.DB, ix.Vault, &c); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "loads the whole file") {
		t.Fatalf("the ruling was dropped rather than flagged:\n%s", out)
	}
	if !strings.Contains(out, "⚠ recorded at "+anchor+"; internal/parse/reader.go changed in 1 commit since") {
		t.Errorf("the ruling about a rewritten file is handed over as settled:\n%s", out)
	}
	if strings.Count(out, "may no longer hold") != 1 {
		t.Errorf("a ruling about a file nobody touched since was flagged too:\n%s", out)
	}

	// The same ruling, now in history behind a newer checkpoint, keeps its flag.
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "api", Agent: "claude", Task: "something else", TS: now.Add(-time.Minute).Unix(), Git: git,
	}); err != nil {
		t.Fatal(err)
	}
	p, err = Build(ix, nil, "", Request{Task: "continue", Hint: "api", Dir: repo, Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out = p.Render()
	if !strings.Contains(out, "out of memory at 2GB — ⚠ recorded at "+anchor) {
		t.Errorf("an earlier ruling about a rewritten file lost its flag:\n%s", out)
	}
}
