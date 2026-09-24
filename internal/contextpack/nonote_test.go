package contextpack

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// Nothing in the product writes a project note, so saying one is missing told
// every reader, on every restore, of a thing that could be there and never
// would be (#133). A project with a work history is named, and the history
// speaks for itself.
func TestAProjectWithOnlyAWorkHistoryIsNotToldItLacksANote(t *testing.T) {
	p := &Pack{Hint: "shop", Checkpoint: &session.Checkpoint{Project: "shop"}}
	var b strings.Builder
	p.renderHeader(&b)
	out := b.String()
	if strings.Contains(out, "no note") {
		t.Errorf("the header promises a note nothing writes:\n%s", out)
	}
	if !strings.Contains(out, "Project **shop**") {
		t.Errorf("the header lost the project's name:\n%s", out)
	}
}
