package contextpack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #177: every heading in a context pack is clipped by oneLine, which cut at a
// byte count — a Japanese project name rendered with "�" where the cut landed.
func TestAClippedLabelInAnyScriptIsValidText(t *testing.T) {
	if got := oneLine("a" + strings.Repeat("日本", 100)); !utf8.ValidString(got) {
		t.Errorf("oneLine split a character: %q", got)
	}
}
