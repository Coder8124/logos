package main

import (
	"bytes"
	"strings"
	"testing"
)

// Setup ended by asking the user to restart "the host", type "checkpoint this:
// trying X, ruled out Y because Z, next step is W" and then "resume <project>".
// It had just wired the apps by name, and the user was left to invent a
// scenario and guess what the project is filed under.
func TestSetupsClosingStepsNameTheWiredHostsAndHaveNoPlaceholders(t *testing.T) {
	var out bytes.Buffer
	tryTheHandoff(&out, []string{"Claude Code", "Cursor"}, "logos", "")
	got := out.String()

	for _, want := range []string{"Restart Claude Code and Cursor", "In Claude Code", "In Cursor", "logos resume"} {
		if !strings.Contains(got, want) {
			t.Errorf("closing steps do not say %q:\n%s", want, got)
		}
	}
	for _, placeholder := range []string{"<project>", "trying X", "the host,"} {
		if strings.Contains(got, placeholder) {
			t.Errorf("closing steps still carry the placeholder %q:\n%s", placeholder, got)
		}
	}
}

// With one host wired, the second agent is a fresh session of the same app, not
// "another" agent the user may not have.
func TestWithOneHostWiredTheHandoffIsBetweenTwoSessionsOfIt(t *testing.T) {
	var out bytes.Buffer
	tryTheHandoff(&out, []string{"Cursor"}, "npx @noeton/logos", "for a `logos` command, run `npm i -g @noeton/logos`")
	got := out.String()

	for _, want := range []string{"Restart Cursor", "In Cursor", "new Cursor session", "npx @noeton/logos resume", "npm i -g"} {
		if !strings.Contains(got, want) {
			t.Errorf("closing steps do not say %q:\n%s", want, got)
		}
	}
}

// Claude Desktop has no repository open, so a checkpoint there has no project
// to be filed under. The steps write it from a host that has one, and Desktop
// only resumes.
func TestClaudeDesktopIsNeverAskedToCheckpointARepository(t *testing.T) {
	var out bytes.Buffer
	tryTheHandoff(&out, []string{"Claude Desktop", "Cursor"}, "logos", "")
	got := out.String()
	if !strings.Contains(got, `1. In Cursor, in a repository`) || !strings.Contains(got, `2. In Claude Desktop, say: "resume"`) {
		t.Errorf("the checkpoint should come from Cursor and the resume from Claude Desktop:\n%s", got)
	}

	out.Reset()
	tryTheHandoff(&out, []string{"Claude Desktop"}, "logos", "")
	if got := out.String(); strings.Contains(got, "repository you are working in") || !strings.Contains(got, "under the project") {
		t.Errorf("with only Claude Desktop, the checkpoint must name its project:\n%s", got)
	}
}
