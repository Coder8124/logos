package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standIn puts the test in a directory named after a project, with a fresh
// vault, so the continuity verbs have a cwd to infer from.
func standIn(t *testing.T, project string) string {
	t.Helper()
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv("BRAIN_PROJECT", "")

	work := filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
	return vaultDir
}

// `brain checkpoint` ends by telling the reader "`brain resume` picks it up
// now". That instruction was wrong: a bare resume answered with a usage error,
// so the very first handoff a new user performs failed on the product's own
// advice. Standing in the project directory is enough to say which project.
func TestABareResumePicksUpTheProjectYouAreStandingIn(t *testing.T) {
	standIn(t, "widgets")

	if err := runCheckpoint([]string{"--task", "cache layer", "--next", "try an LRU"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runResume(nil); err != nil {
			t.Fatalf("resume: %v", err)
		}
	})

	if !strings.Contains(out, "widgets") || !strings.Contains(out, "try an LRU") {
		t.Errorf("bare resume did not pick up the project in this directory:\n%s", out)
	}
}

// A checkpoint written with flags only used to file itself under a project
// literally called "--task", because the first argument was read as the project
// name without checking whether it was a flag.
func TestAFlagIsNeverMistakenForTheProjectName(t *testing.T) {
	vaultDir := standIn(t, "widgets")

	if err := runCheckpoint([]string{"--task", "cache layer"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "--task")); err == nil {
		t.Fatal("the checkpoint was filed under a project named after a flag")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "widgets")); err != nil {
		t.Fatalf("the checkpoint was not filed under the project in this directory: %v", err)
	}
}

// BRAIN_PROJECT outranks the directory for the CLI exactly as it does for the
// MCP server; two folders the user deliberately joined must not split apart
// depending on which one they happen to be standing in.
func TestBrainProjectOutranksTheDirectory(t *testing.T) {
	vaultDir := standIn(t, "widgets")
	t.Setenv("BRAIN_PROJECT", "gadgets")

	if err := runCheckpoint([]string{"--task", "cache layer"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "sessions", "gadgets")); err != nil {
		t.Fatalf("BRAIN_PROJECT did not win over the directory: %v", err)
	}
}

// A bare resume still has to read its own flags. The project positional and the
// flags share one argument list, so dropping the positional must not shift the
// index the flag values are read from.
func TestABareResumeStillReadsItsFlags(t *testing.T) {
	standIn(t, "widgets")
	if err := runCheckpoint([]string{"--task", "cache layer", "--next", "try an LRU"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runResume([]string{"--budget", "900"}); err != nil {
			t.Fatalf("resume: %v", err)
		}
	})

	if !strings.Contains(out, "of 900 tokens") {
		t.Errorf("--budget was not honoured on a bare resume:\n%s", out)
	}
}

// `brain resume`'s own empty-state message tells a new user to run
// `brain note myapp "what you just did"` — it worked out the project from the
// directory in order to print that line, and then note demanded it typed again.
// A single argument is the note; the project is where you are standing.
func TestANoteWithNoProjectNamedIsFiledUnderTheProjectYouAreStandingIn(t *testing.T) {
	standIn(t, "widgets")

	if err := runNote([]string{"rewired the cache"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runResume(nil); err != nil {
			t.Fatalf("resume: %v", err)
		}
	})
	if !strings.Contains(out, "rewired the cache") {
		t.Errorf("the note was not filed under the project in this directory:\n%s", out)
	}
}

// Naming the project explicitly still wins, and still means what it always did.
func TestANoteStillTakesAnExplicitProjectName(t *testing.T) {
	standIn(t, "widgets")

	if err := runNote([]string{"gadgets", "rewired the cache"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runResume([]string{"gadgets"}); err != nil {
			t.Fatalf("resume: %v", err)
		}
	})
	if !strings.Contains(out, "rewired the cache") {
		t.Errorf("an explicitly named project no longer takes the note:\n%s", out)
	}
}

// `brain context "<task>"` standing inside a project answered "no project
// matched" and served standing context only, while `brain resume` one line
// earlier had no trouble knowing where it was. The brief a user reads to judge
// retrieval was the one command that would not look around itself.
func TestContextTakesTheProjectFromTheDirectoryWhenNoneIsNamed(t *testing.T) {
	standIn(t, "widgets")

	if err := runCheckpoint([]string{"--task", "cache layer", "--next", "try an LRU"}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runContext([]string{"speed up the cache"}); err != nil {
			t.Fatalf("context: %v", err)
		}
	})
	if !strings.Contains(out, "try an LRU") {
		t.Errorf("context did not find the project in this directory:\n%s", out)
	}
}
