package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/exclude"
	"github.com/Coder8124/logos/internal/session"
)

// erasableProject files two checkpoints and an archived transcript for a
// project folder named "erasable", plus a worktree scope holding one more of
// each, and returns the project dir and the vault's folder for it.
func erasableProject(t *testing.T, vaultDir string) (dir, root string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "erasable")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"first zebracorn task", "second zebracorn task"} {
		if _, err := session.WriteAuto(vaultDir, session.Checkpoint{
			Project: "erasable", Task: task, State: "zebracorn state", HostSession: "chat-" + task[:3],
		}); err != nil {
			t.Fatal(err)
		}
	}
	root = filepath.Join(vaultDir, session.CheckpointDir, "erasable")
	convo := filepath.Join(root, "some-chat")
	wt := filepath.Join(root, "wt-feature", "other-chat")
	for _, d := range []string{convo, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(convo, session.TranscriptStem+".md"), "zebracorn transcript")
	put(t, filepath.Join(wt, session.TranscriptStem+".md"), "zebracorn worktree transcript")
	put(t, filepath.Join(root, "wt-feature", "20260101-000000-wt.md"), "---\nproject: erasable\n---\nzebracorn")
	return dir, root
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestErasingAProjectRemovesItsTranscriptsAndCheckpointsAndSaysHowMany(t *testing.T) {
	vaultDir := scratchIngest(t)
	dir, root := erasableProject(t, vaultDir)
	before, _ := exclude.KeptForProject(vaultDir, dir)
	if before.Transcripts == 0 || before.Checkpoints == 0 {
		t.Fatalf("the fixture kept nothing to erase: %+v", before)
	}
	out := captureStdout(t, func() {
		if err := runExclude([]string{"--project", dir, "--erase"}); err != nil {
			t.Fatal(err)
		}
	})
	want := "erased 2 archived transcript(s) and 3 checkpoint(s) of project"
	if !strings.Contains(out, want) {
		t.Errorf("the erase did not say its counts (want %q):\n%s", want, out)
	}
	if after, _ := exclude.KeptForProject(vaultDir, dir); after != (exclude.Kept{}) {
		t.Errorf("the project still keeps %+v", after)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the emptied project folder was left behind (%v)", err)
	}
	if got := exclude.Check(vaultDir, exclude.Subject{Cwd: dir}); got == "" {
		t.Error("the erased project is no longer excluded")
	}
}

func TestErasingAProjectLeavesFilesItDoesNotRecognise(t *testing.T) {
	vaultDir := scratchIngest(t)
	dir, root := erasableProject(t, vaultDir)
	keep := []string{
		filepath.Join(root, "some-chat", "notes.md"),
		filepath.Join(root, "wt-feature", "other-chat", "scratch.txt"),
		filepath.Join(root, "review-queue.md"),
	}
	for _, p := range keep {
		put(t, p, "mine")
	}
	captureStdout(t, func() {
		if err := runExclude([]string{"--project", dir, "--erase"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a file the erase does not own was removed: %v", err)
		}
	}
}

func TestErasingAProjectAlsoRemovesItsWorktreeScopes(t *testing.T) {
	vaultDir := scratchIngest(t)
	dir, root := erasableProject(t, vaultDir)
	k, _, err := exclude.EraseProject(vaultDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if k.Transcripts != 2 || k.Checkpoints != 3 {
		t.Errorf("counts = %+v, want 2 transcripts and 3 checkpoints (worktree scope included)", k)
	}
	if _, err := os.Stat(filepath.Join(root, "wt-feature")); !os.IsNotExist(err) {
		t.Errorf("the worktree scope folder remains (%v)", err)
	}
}

func TestAnErasedProjectIsGoneFromTheIndex(t *testing.T) {
	vaultDir := scratchIngest(t)
	dir, _ := erasableProject(t, vaultDir)
	ix, err := openIndexQuiet()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	if hits, _ := ix.LexicalSearch("zebracorn", 10); len(hits) == 0 {
		t.Fatal("the fixture was never indexed, so its absence proves nothing")
	}
	ix.Close()
	captureStdout(t, func() {
		if err := runExclude([]string{"--project", dir, "--erase"}); err != nil {
			t.Fatal(err)
		}
	})
	ix, err = openIndexQuiet()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if hits, _ := ix.LexicalSearch("zebracorn", 10); len(hits) != 0 {
		t.Errorf("the index still returns %d hit(s) for the erased project", len(hits))
	}
}

func TestAFailedProjectEraseReportsWhatWasAlreadyRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	vaultDir := scratchIngest(t)
	dir, root := erasableProject(t, vaultDir)
	// The worktree folder cannot be written to, so its files cannot go; the
	// project's own files are visited first and do.
	locked := filepath.Join(root, "wt-feature")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	var runErr error
	out := captureStdout(t, func() { runErr = runExclude([]string{"--project", dir, "--erase"}) })
	if runErr == nil {
		t.Fatal("a failed removal was reported as success")
	}
	if !strings.Contains(out, "erased before failing:") {
		t.Errorf("the partial erase was not announced:\n%s", out)
	}
	if strings.Contains(out, "erased before failing: 0 archived transcript(s), 0 checkpoint(s)") {
		t.Errorf("the counts so far were lost:\n%s", out)
	}
}
