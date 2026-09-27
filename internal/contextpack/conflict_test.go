package contextpack

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

func prices() []memory.Memory {
	return []memory.Memory{
		{ID: 1, Created: 100, Text: "We are targeting a $199 retail price."},
		{ID: 2, Created: 200, Text: "Retail is moving to $229 after the optics quote came back."},
		{ID: 3, Created: 300, Text: "Final call: retail price is $249. That is locked for launch."},
	}
}

func keptTexts(kept []memory.Memory) string {
	var out []string
	for _, m := range kept {
		out = append(out, m.Text)
	}
	return strings.Join(out, " | ")
}

// The $229 line shares one word with the $249 line, so only the question tied
// them together — and only when it said "retail price". Asked any other way,
// the superseded figure was handed over beside the current one (#223).
func TestASupersededPriceIsDroppedHoweverTheQuestionIsWorded(t *testing.T) {
	for _, task := range []string{
		"what is the retail price?",
		"how much will the glasses sell for?",
		"what's our price point?",
		"what are we charging customers?",
	} {
		kept, _ := supersede(task, prices())
		got := keptTexts(kept)
		if strings.Contains(got, "$199") || strings.Contains(got, "$229") || !strings.Contains(got, "$249") {
			t.Errorf("asked %q, kept: %s", task, got)
		}
	}
}

// A chain of restated prices must not swallow a different fact that happens to
// share one of its words and carry a number: a false supersession silently
// deletes a true fact.
func TestAFactSharingOneWordWithAPriceChainIsNotSuperseded(t *testing.T) {
	mems := append(prices(), memory.Memory{ID: 4, Created: 150, Text: "Retail staff at the flagship are paid $31 an hour."})
	kept, _ := supersede("what are we charging customers?", mems)
	if !strings.Contains(keptTexts(kept), "$31") {
		t.Errorf("the staff wage was dropped as a superseded price: %s", keptTexts(kept))
	}
}
