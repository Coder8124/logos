package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

// repoToSetUpIn is a fresh repository with commits commits, made the working
// directory for the rest of the test — where a new user runs setup.
func repoToSetUpIn(t *testing.T, name string, commits int) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "--initial-branch=main")
	git("config", "user.email", "ada@example.com")
	git("config", "user.name", "Ada Lovelace")
	git("config", "commit.gpgsign", "false")
	for i := 0; i < commits; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", fmt.Sprintf("add f%d", i))
	}
	t.Setenv("LOGOS_PROJECT", "")
	t.Chdir(dir)
	return dir
}

// A list of ✓s says config files were written; it does not say the next agent
// will know anything. Setup ends on the resume that agent will be handed, for
// the repository it was run in, and it has to get there whether or not a model
// runtime is on the machine — most first runs have none.
func TestSetupFromARepositoryEndsWithAResumeNamingTheProject(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime func(t *testing.T)
	}{
		{"with no model runtime", func(t *testing.T) { t.Setenv("LOGOS_RUNTIME", "off") }},
		{"with a model runtime", func(t *testing.T) {
			rr := newRecordingRuntime(t, "nomic-embed-text")
			t.Setenv("LOGOS_RUNTIME", "")
			old := provider.LocalEndpoints
			provider.LocalEndpoints = []provider.LocalEndpoint{{Name: "Fake", URL: rr.URL + "/v1"}}
			t.Cleanup(func() { provider.LocalEndpoints = old })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupInFakeHome(t)
			fakeHosts(t, "Fakey Desktop")
			tc.runtime(t)
			repoToSetUpIn(t, "orchard", 1)

			out := captureStdout(t, func() {
				if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
					t.Fatalf("setup: %v", err)
				}
			})

			if !strings.Contains(out, "$ logos resume orchard") {
				t.Fatalf("setup did not end on a resume of the repository it ran in:\n%s", out)
			}
			resume := out[strings.Index(out, "$ logos resume orchard"):]
			for _, want := range []string{"## Where we left off", setupAgent, "Connect Logos to the agents", "**Next step:**"} {
				if !strings.Contains(resume, want) {
					t.Errorf("resume excerpt lacks %q:\n%s", want, resume)
				}
			}
			if !strings.HasSuffix(strings.TrimSpace(out), "in this repository.") {
				t.Errorf("setup does not end by saying what the excerpt is:\n%s", out)
			}
			if n := checkpointsUnder(filepath.Join(dir, session.CheckpointDir, "orchard")); n != 1 {
				t.Errorf("%d checkpoints in the vault for orchard, want the 1 setup wrote", n)
			}
		})
	}
}

// Setup's line marks where recording began. On a project that already has
// work in it, filing it would make "where we left off" say setup — burying the
// real handoff under the very demonstration meant to show it off.
func TestSetupWritesNoCheckpointForAProjectThatAlreadyHasOne(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")
	t.Setenv("LOGOS_RUNTIME", "off")
	repoToSetUpIn(t, "orchard", 1)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project: "orchard", Agent: "claude-code", Task: "prune the grafting code",
		State: "halfway through the grafting rewrite", Next: "finish the rootstock table",
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	if n := checkpointsUnder(filepath.Join(dir, session.CheckpointDir, "orchard")); n != 1 {
		t.Errorf("%d checkpoints for orchard after setup, want only the agent's 1", n)
	}
	if !strings.Contains(out, "none written by setup") {
		t.Errorf("setup did not say why it wrote no checkpoint:\n%s", out)
	}
	if !strings.Contains(out, "finish the rootstock table") {
		t.Errorf("the resume shown is not the agent's handoff:\n%s", out)
	}
}

// Memories derived from history are recalled later as if the user had said
// them. --yes agrees to wiring hosts, not to claims about the user's project
// they never read, so it points at `logos bootstrap` and writes none.
func TestSetupWithYesWritesNoMemoriesFromHistory(t *testing.T) {
	dir := setupInFakeHome(t)
	fakeHosts(t, "Fakey Desktop")
	t.Setenv("LOGOS_RUNTIME", "off")
	repoToSetUpIn(t, "orchard", 24)

	out := captureStdout(t, func() {
		if err := setupCmd([]string{"--vault", dir, "--yes"}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})

	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if n, err := memory.Count(ix.DB); err != nil || n != 0 {
		t.Errorf("setup --yes wrote %d memories (err %v), want none", n, err)
	}
	if !strings.Contains(out, "`logos bootstrap` shows them first") {
		t.Errorf("setup --yes did not point at the history it left unread:\n%s", out)
	}
}
