package mcpserver

import (
	"strings"
	"testing"
)

// The receipt said "noted in logos" whatever the project was. On the
// repository called logos — and on this one, whose project is brain — that
// read as the note having been filed in the wrong project, and was reported as
// a misfiling bug twice when the note was exactly where it was asked to go.
func TestANotesReceiptNamesTheProjectItWasFiledUnder(t *testing.T) {
	c, _, _ := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "1"},
	})
	c.notify("notifications/initialized", nil)

	out, isErr := c.callText(t, "note_progress", map[string]any{
		"project": "brain", "text": "the flaky test is a timezone bug",
	})
	if isErr {
		t.Fatalf("note_progress reported error: %s", out)
	}
	if !strings.Contains(out, "noted under brain") {
		t.Errorf("receipt = %q, want it to name the project brain", out)
	}
	if strings.Contains(out, "noted in logos") {
		t.Errorf("receipt = %q still names the product where the project goes", out)
	}
}

// The masking sentence was appended with a bare space, so a note carrying a
// token came back "…until you checkpoint 1 secret-shaped token(s) redacted":
// one sentence that reads as the checkpoint being what was redacted.
func TestANotesReceiptSaysWhatWasMaskedInASentenceOfItsOwn(t *testing.T) {
	c, _, _ := startServer(t)
	c.req("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "cursor", "version": "1"},
	})
	c.notify("notifications/initialized", nil)

	out, isErr := c.callText(t, "note_progress", map[string]any{
		"project": "brain", "text": "upload works with GITHUB_TOKEN=ghp_R2d2C3poBb8Ee9Ff0Gg1Hh2Ii3Jj4Kk5Ll6M",
	})
	if isErr {
		t.Fatalf("note_progress reported error: %s", out)
	}
	if !strings.Contains(out, "until you checkpoint. 1 secret-shaped token(s) redacted") {
		t.Errorf("receipt = %q, want the masking said in a sentence of its own", out)
	}
}
