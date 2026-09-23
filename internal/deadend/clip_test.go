package deadend

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #177: the same byte cut as contextpack's oneLine, in the dead-end renderer.
func TestADeadEndLabelInAnyScriptIsValidText(t *testing.T) {
	if got := oneLine("a" + strings.Repeat("日本", 100)); !utf8.ValidString(got) {
		t.Errorf("oneLine split a character: %q", got)
	}
}
