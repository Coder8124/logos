package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A verb that fails with a usage error where it could have done the obvious
// thing reads as a broken tool to a first-time user. `brain note`, `brain
// resume`, `brain checkpoint` and `brain sessions` all required an explicit
// project name, even though every one of them can work it out from the
// directory the user is already standing in — exactly as `brain project-name`
// does.
func TestProjectArgFallsBackToTheDirectoryYouAreStandingIn(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	t.Setenv("BRAIN_PROJECT", "standing-here")

	project, rest := projectArg([]string{"--task", "did a thing"})
	if project != "standing-here" {
		t.Errorf("project = %q, want the cwd's project when none was named", project)
	}
	if len(rest) != 2 || rest[0] != "--task" {
		t.Errorf("rest = %v, a leading flag must not be consumed as the project", rest)
	}

	// An explicit project still wins over the cwd.
	project, rest = projectArg([]string{"named-project", "--task", "x"})
	if project != "named-project" {
		t.Errorf("an explicit project argument was overridden by the cwd fallback: %q", project)
	}
	if len(rest) != 2 {
		t.Errorf("rest = %v, want the project argument stripped", rest)
	}
}

// `brain doctor` prints a FAILED row and still exits 0, which is invisible to
// anything that reads the exit code rather than the terminal — a pre-flight
// `brain doctor && brain resume proj` in a script proceeds past a vault the
// report just said was broken.
func TestDoctorFailsExitCodeWhenACheckFails(t *testing.T) {
	if err := doctorVerdict(0); err != nil {
		t.Errorf("doctorVerdict(0) = %v, want nil when nothing failed", err)
	}
	if err := doctorVerdict(2); err == nil {
		t.Error("doctorVerdict(2) = nil, want an error naming the failure count")
	}
}

// A search with no hits printed nothing at all — indistinguishable, from the
// terminal, between "ran and found nothing" and "silently did not run".
func TestSearchOnAnEmptyIndexSaysNothingMatched(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("BRAIN_VAULT", vaultDir)
	// openIndex creates .brain/index.db on first open; no notes are indexed.
	if err := os.MkdirAll(filepath.Join(vaultDir, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_EMBED", "off")

	out := captureStdout(t, func() {
		if err := search("nothing will match this"); err != nil {
			t.Fatal(err)
		}
	})
	if out == "" {
		t.Error("a zero-hit search printed nothing at all — indistinguishable from a crash")
	}
}
