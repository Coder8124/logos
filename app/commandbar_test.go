package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// The command bar is a dispatcher, not a second implementation — it must
// call the same Insights binding the panel already renders from, sources
// and all, rather than reaching into internal/insight itself.
func TestCommandBarRunsInsightsAndCitesTheCheckpoint(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	ix, err := a.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "first",
		Blockers: []string{"the vendor has not shipped the connector firmware"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "second",
		Blockers: []string{"still waiting on the connector firmware from the vendor"},
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	result, err := a.RunCommand("insights kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verb != "insights" {
		t.Fatalf("expected the insights verb to be recorded, got %q", result.Verb)
	}
	if !strings.Contains(result.Output, "connector firmware") {
		t.Fatalf("expected the recurring blocker's text in the output, got:\n%s", result.Output)
	}
}

// A command that takes no argument still announces a count (invariant 3) —
// the bar is not exempt from the rule the CLI and app views already follow.
func TestCommandBarListsProjectsWithACount(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	result, err := a.RunCommand("projects")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verb != "projects" {
		t.Fatalf("expected the projects verb to be recorded, got %q", result.Verb)
	}
	if !strings.Contains(result.Output, "0 project") {
		t.Fatalf("expected the zero count spelled out on an empty vault, got:\n%s", result.Output)
	}
}

// A typo must not fail silently — it names the nearest known verb so the user
// can correct course, rather than returning an empty result that looks the
// same as a command that ran and found nothing.
func TestCommandBarOnATypoNamesTheNearestKnownVerbInsteadOfFailingSilently(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	result, err := a.RunCommand("insigths")
	if err != nil {
		t.Fatal(err)
	}
	if result.Suggested != "insights" {
		t.Fatalf("expected insights suggested for a near-miss typo, got %q", result.Suggested)
	}
	if result.Output != "" {
		t.Fatalf("expected no output for an unresolved verb, got %q", result.Output)
	}
}

// Blank input is not a typo of anything — the bar should say so without
// guessing a nearest verb that has nothing to do with an empty string.
func TestCommandBarOnBlankInputReportsNoVerbRatherThanGuessingOne(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	result, err := a.RunCommand("   ")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verb != "" || result.Suggested != "" {
		t.Fatalf("expected blank input to resolve to nothing, got %+v", result)
	}
}

// The whole point of routing through bound Go methods is that an unknown
// verb list is discoverable — Commands() is what the frontend renders as the
// "/" hint list, so it must not be empty and must not silently drop entries.
func TestCommandBarListsEveryKnownVerbForTheFrontendHintMenu(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	cmds := a.Commands()
	if len(cmds) == 0 {
		t.Fatal("expected at least one known command")
	}
	var sawInsights bool
	for _, c := range cmds {
		if c.Name == "insights" {
			sawInsights = true
		}
		if c.Usage == "" {
			t.Fatalf("command %q has no usage text", c.Name)
		}
	}
	if !sawInsights {
		t.Fatal("expected insights among the known commands")
	}
}
