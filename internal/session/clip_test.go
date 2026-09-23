package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #177: the title was clipped at 70 bytes, which lands inside a character for
// any non-Latin task — and the title is written into the checkpoint on disk.
func TestACheckpointTitleInAnyScriptIsValidText(t *testing.T) {
	title := Checkpoint{Project: "p", Task: "ab" + strings.Repeat("日本", 40)}.title()
	if !utf8.ValidString(title) {
		t.Errorf("the title written to the vault is not valid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Errorf("a clipped title should say so: %q", title)
	}
}
