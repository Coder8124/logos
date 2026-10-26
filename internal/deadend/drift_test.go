package deadend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// before_you_try is asked at the moment an agent is about to act, and a ruling
// about a file rewritten since is the one most likely to stop it doing the
// thing that now works. Found, kept, and said to have moved.
func TestBeforeYouTryFlagsARulingWhoseFileChangedSince(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	_, anchor := gitstate.Head(repo)

	dir, db := seed(t)
	if err := session.Commit(db, dir, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude", Task: "work", Next: "carry on",
		Git:    gitstate.State{Commit: anchor},
		Failed: []string{"streaming the vendor export — internal/parse/reader.go loads the whole file into memory"},
	}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")

	hits, err := Check(dir, db, nil, "", "streaming the vendor export", "kestrel-one", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the ruling should still be found")
	}
	if hits[0].Commit != anchor {
		t.Fatalf("the ruling lost the commit its checkpoint recorded: %q, want %q", hits[0].Commit, anchor)
	}
	MarkDrift(hits, repo)
	out := Render("streaming the vendor export", hits)
	if !strings.Contains(out, "⚠ recorded at "+anchor+"; internal/parse/reader.go changed in 1 commit since") {
		t.Errorf("a ruling about a rewritten file is presented as settled:\n%s", out)
	}
	// The seeded rulings carry no commit and name no file in this repository.
	if strings.Count(out, "may no longer hold") != 1 {
		t.Errorf("rulings with nothing to measure were flagged:\n%s", out)
	}
}

// What `logos review --stale` decided reaches before_you_try: a ruling a
// person retired is not a dead end any more, and one they re-confirmed is
// measured from the commit they confirmed it at, so its warning is answered.
func TestBeforeYouTryHonoursWhatReviewStaleDecided(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	commitFile(t, repo, "internal/parse/reader.go", "load all\n")
	_, anchor := gitstate.Head(repo)
	dir, db := seed(t)
	retired := "streaming the vendor export — internal/parse/reader.go loads the whole file into memory"
	confirmed := "mmap the vendor export — internal/parse/reader.go needs a seekable source"
	if err := session.Commit(db, dir, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude", Task: "work", Next: "carry on",
		Git: gitstate.State{Commit: anchor}, Failed: []string{retired, confirmed},
	}); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "internal/parse/reader.go", "read in chunks\n")
	_, head := gitstate.Head(repo)
	if err := session.Retire(dir, "kestrel-one", retired); err != nil {
		t.Fatal(err)
	}
	if err := session.Reconfirm(dir, "kestrel-one", confirmed, head); err != nil {
		t.Fatal(err)
	}

	all, err := Collect(dir, db, "kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range all {
		if strings.Contains(r.Record.Raw, "loads the whole file") {
			t.Errorf("a retired ruling is still a dead end: %+v", r)
		}
		if strings.Contains(r.Record.Raw, "seekable source") {
			found = true
			if r.Commit != head {
				t.Errorf("a re-confirmed ruling is measured from %q, want %q", r.Commit, head)
			}
			hits := []Ruling{r}
			MarkDrift(hits, repo)
			if hits[0].Drift != nil {
				t.Errorf("a ruling re-confirmed at head still drifts: %s", hits[0].Drift.Note())
			}
		}
	}
	if !found {
		t.Fatal("a re-confirmed ruling was dropped")
	}
}
