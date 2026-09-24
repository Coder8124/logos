package main

import (
	"strings"
	"testing"
	"time"
)

// #165: `logos loop add ""` answered "tracked" and a bare "- " bullet then sat
// under Still open in every resume. `note ""` and `memory add ""` refuse it.
func TestAnEmptyLoopIsRefusedRatherThanTracked(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")

	for _, text := range []string{"", "   "} {
		if err := commitmentCmd([]string{"add", text}); err == nil {
			t.Errorf("loop add %q was tracked", text)
		}
	}
	out := captureStdout(t, func() {
		if err := commitmentCmd([]string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "[1]") {
		t.Errorf("a blank loop reached the list:\n%s", out)
	}
}

// #167: every gap under an hour read "Since you've been away (hour)", and a
// gap of a day "(day)" — every other span carries a count.
func TestTheAwaySpanAlwaysReadsAsAPhrase(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "under an hour",
		59 * time.Minute: "under an hour",
		3 * time.Hour:    "3 hours",
		30 * time.Hour:   "a day",
		72 * time.Hour:   "3 days",
	} {
		if got := humanizeAway(d); got != want {
			t.Errorf("humanizeAway(%v) = %q, want %q", d, got, want)
		}
	}
}

// #168: `logos projects` said "2 checkpoints across 2 projects: escape, proj"
// when escape held one working note and no checkpoint. The checkpoint count
// and the list it introduces have to be about the same projects.
func TestProjectsDoesNotCountANotesOnlyScopeAsACheckpointProject(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")

	if err := runCheckpoint([]string{"proj", "--task", "ship it"}); err != nil {
		t.Fatal(err)
	}
	if err := runNote([]string{"escape", "a note and nothing else"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := projectsCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 checkpoint across 1 project: proj") {
		t.Errorf("the checkpoint sentence should name only proj:\n%s", out)
	}
	if !strings.Contains(out, "escape") {
		t.Errorf("the notes-only project should still be named, separately:\n%s", out)
	}
}

// #168: `logos graph nonexistent` drew a ghost node "missing deg 0" and exited 0.
func TestGraphOfAFocusThatIsNotInTheVaultSaysSo(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")

	err := runGraph("nonexistent", 2, false)
	if err == nil || !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("an unknown focus should be refused by name, got %v", err)
	}
	if err := runGraph("", 2, false); err == nil {
		t.Errorf("an empty vault drew a graph of today's missing daily note")
	}
}
