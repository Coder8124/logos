package main

import (
	"strings"
	"testing"
)

// `brain project <name>` reads the activity-rollup dossier (project.Get, via
// openEvents), a different subsystem from the vault checkpoints/memory that
// `sessions`/`continuity`/`memory log` read directly. Before this fix, a
// project with real checkpoints but no rollup dossier yet got the same
// "no project matching" message as a name nobody has ever used — which reads
// as "this project doesn't exist" and is false.
func TestProjectCommandDistinguishesNoDossierFromNoProject(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)

	if err := runCheckpoint([]string{"demo", "--task", "explore the CLI"}); err != nil {
		t.Fatalf("seeding a checkpoint: %v", err)
	}

	err := projectCmd([]string{"demo"})
	if err == nil {
		t.Fatal("no rollup dossier exists yet, so this should still error")
	}
	if !strings.Contains(err.Error(), "no activity dossier") {
		t.Errorf("a project with real checkpoints should not be reported as unknown, got: %v", err)
	}

	err = projectCmd([]string{"never-heard-of-this-project"})
	if err == nil || !strings.Contains(err.Error(), "no project matching") {
		t.Errorf("a genuinely unknown project should still say so plainly, got: %v", err)
	}
}
