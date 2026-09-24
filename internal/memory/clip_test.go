package memory

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #177: graph labels and the recall summary were cut by bytes too.
func TestMemoryLabelsInAnyScriptAreValidText(t *testing.T) {
	long := "a" + strings.Repeat("日本", 100)
	if got := sanitizeLabel(long); !utf8.ValidString(got) {
		t.Errorf("sanitizeLabel split a character: %q", got)
	}
	if got := topText([]Memory{{Text: long}}); !utf8.ValidString(got) {
		t.Errorf("topText split a character: %q", got)
	}
}
