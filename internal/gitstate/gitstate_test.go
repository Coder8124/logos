package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The contract that matters most is not what this reads when git cooperates —
// it is that it never fails when git does not. A checkpoint that refuses to save
// because a directory was not a repository would trade a missing sha for lost
// work, which is the wrong way round.

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func commit(t *testing.T, dir, name, body, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", message}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestReadsBranchAndCommit(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "the first commit")

	s := Read(dir)
	if s.Empty() {
		t.Fatal("a repository with a commit reported no state")
	}
	if s.Branch != "main" {
		t.Errorf("branch = %q, want main", s.Branch)
	}
	if len(s.Commit) < 6 {
		t.Errorf("commit = %q, want a short sha", s.Commit)
	}
	if s.Subject != "the first commit" {
		t.Errorf("subject = %q", s.Subject)
	}
	if s.Dirty != 0 {
		t.Errorf("dirty = %d on a clean tree, want 0", s.Dirty)
	}
	// "clean at this sha" is a much stronger handoff than "near this sha", so
	// the summary has to say it.
	if !strings.Contains(s.Summary(), "clean") {
		t.Errorf("summary %q does not report a clean tree", s.Summary())
	}
}

// The evidence half: what the session actually touched, observed rather than
// reported. An agent's own Files list is a claim; this is not.
func TestReadsUncommittedWork(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\ntwo\nthree\n", "base")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nCHANGED\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Read(dir)
	if s.Dirty != 2 {
		t.Errorf("dirty = %d, want 2 (one modified, one untracked)", s.Dirty)
	}
	found := map[string]bool{}
	for _, f := range s.Files {
		found[f] = true
	}
	for _, want := range []string{"a.txt", "b.txt"} {
		if !found[want] {
			t.Errorf("%s missing from %v", want, s.Files)
		}
	}
	// Tracked-file diff only: counting an untracked file's whole length as
	// insertions would overstate the change.
	if s.Insertions != 1 || s.Deletions != 1 {
		t.Errorf("diffstat = +%d/-%d, want +1/-1", s.Insertions, s.Deletions)
	}
}

// Every one of these is a normal condition, not a failure, and each must
// produce an empty State rather than a panic or an error.
func TestDegradesEverywhere(t *testing.T) {
	for _, name := range []string{"not a repo", "missing directory", "empty path"} {
		var dir string
		switch name {
		case "not a repo":
			dir = t.TempDir()
		case "missing directory":
			dir = filepath.Join(t.TempDir(), "nope")
		case "empty path":
			dir = ""
		}
		s := Read(dir)
		if !s.Empty() {
			t.Errorf("%s: reported state %+v, want empty", name, s)
		}
		if s.Summary() != "" {
			t.Errorf("%s: summary = %q, want empty", name, s.Summary())
		}
	}
}

// A fresh repository with no commits at all: git exits non-zero for HEAD, which
// must read as "nothing to say" rather than breaking the checkpoint.
func TestRepoWithNoCommits(t *testing.T) {
	dir := repo(t)
	s := Read(dir)
	if s.Commit != "" {
		t.Errorf("commit = %q in a repo with no commits", s.Commit)
	}
	// The branch exists even before the first commit, so state is not required
	// to be empty — only sane.
	if s.Dirty != 0 {
		t.Errorf("dirty = %d in an empty repo", s.Dirty)
	}
}

// A detached HEAD reports "HEAD" from rev-parse, which is not a branch name and
// would be misleading in a handoff.
func TestDetachedHeadReportsNoBranch(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "first")
	commit(t, dir, "a.txt", "two\n", "second")

	sha := strings.TrimSpace(git(dir, "rev-parse", "HEAD"))
	if out, err := exec.Command("git", "-C", dir, "checkout", "--detach", sha).CombinedOutput(); err != nil {
		t.Skipf("could not detach: %v: %s", err, out)
	}

	s := Read(dir)
	if s.Branch != "" {
		t.Errorf("branch = %q on a detached HEAD, want empty", s.Branch)
	}
	if s.Commit == "" {
		t.Error("a detached HEAD still has a commit, which is the useful part")
	}
}

// Worktrees are where continuity breaks hardest — same project, divergent
// parallel state — so a checkpoint has to be able to say which tree it meant.
func TestLinkedWorktreeIsNamed(t *testing.T) {
	dir := repo(t)
	commit(t, dir, "a.txt", "one\n", "first")

	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "side", wt).CombinedOutput(); err != nil {
		t.Skipf("worktree unavailable: %v: %s", err, out)
	}

	main := Read(dir)
	if main.Worktree != "" {
		t.Errorf("the main tree was named as a worktree: %q", main.Worktree)
	}

	side := Read(wt)
	if side.Worktree == "" {
		t.Error("a linked worktree was not identified as one")
	}
	if side.Branch != "side" {
		t.Errorf("worktree branch = %q, want side", side.Branch)
	}
}
