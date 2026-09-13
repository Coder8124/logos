package main

import (
	"os"
	"strings"
	"testing"
)

// The host prompt is all or nothing. Declining it to wire a subset left the
// user to work out each host's --host spelling; the line that does exactly
// that has to be on screen when they say no.
func TestDecliningTheHostPromptPrintsTheCommandThatWiresEachFoundHost(t *testing.T) {
	dir := setupInFakeHome(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeHosts(t, "Fakey Desktop", "Copilot in VS Code")
	withAnswers(t, "n\n")

	out := captureStdout(t, func() {
		if err := mcpInstallCmd([]string{"--vault", dir}); err != nil {
			t.Fatalf("mcp install: %v", err)
		}
	})
	if !strings.Contains(out, "logos mcp install --host fakey-desktop --host copilot-in-vs-code") {
		t.Errorf("declining did not print a --host line for the hosts found:\n%s", out)
	}
}
