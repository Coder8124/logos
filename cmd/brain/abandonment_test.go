package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/router"
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

// The probe was the one check whose result nothing could act on. `failed` is
// totalled before the probe loop runs, so a model that could not load printed
// FAILS TO LOAD and the command still exited 0 — a CI step or a shell
// `brain doctor --probe && ...` walked straight past a runtime it had just been
// told was broken.
func TestAModelThatFailsToLoadCountsTowardsTheDoctorVerdict(t *testing.T) {
	line, failed := probeRow(router.T1, "gemma3:4b", router.Capability{Err: "Ollama returned 400 Bad Request"})
	if !failed {
		t.Error("a model that does not load was not counted as a failure, so doctor --probe exits 0")
	}
	if !strings.Contains(line, "FAILS TO LOAD") {
		t.Errorf("row = %q, want it to say the model failed to load", line)
	}

	// A model that loads but ignores schemas is degraded output, not a broken
	// install, and must not fail the exit code.
	if _, failed := probeRow(router.T2, "qwen3:8b", router.Capability{Loads: true}); failed {
		t.Error("a model that loads but ignores JSON schemas was counted as a failure")
	}
	if _, failed := probeRow(router.T2, "qwen3:8b", router.Capability{Loads: true, StructuredOutput: true}); failed {
		t.Error("a healthy model was counted as a failure")
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
