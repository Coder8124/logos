package main

import "testing"

// Most memories in an existing vault predate the Agent field, and every
// memory the CLI itself writes has none at all — brain memory add is not any
// particular coding agent. The listing has to say so plainly rather than
// leaving a trailing "· " that reads as a rendering bug.
func TestAgentLabelRendersEmptyCleanly(t *testing.T) {
	if got := agentLabel(""); got != "-" {
		t.Errorf("agentLabel(\"\") = %q, want a placeholder, not a blank string", got)
	}
	if got := agentLabel("   "); got != "-" {
		t.Errorf("agentLabel of whitespace-only = %q, want a placeholder", got)
	}
	if got := agentLabel("claude-code"); got != "claude-code" {
		t.Errorf("agentLabel(%q) = %q, want the name unchanged", "claude-code", got)
	}
}
