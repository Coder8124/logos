package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// note_progress is sold on one promise: a line survives the agent's context
// running out. A note whose write to the vault failed does not keep it — the
// user is told .logos is safe to delete — and the agent that was told about the
// failure has since exited. This reader is the next one, and rendering the note
// as though it were on disk is how the promise breaks quietly.
func TestResumeSaysWhichWorkingNotesAreNotYetInTheVault(t *testing.T) {
	now := time.Now()
	p := Pack{
		Task: "resume",
		Working: []session.Note{
			{Text: "Ran the drop test series.", TS: now.Unix()},
			{Text: "vitest needs jsdom for the PricingTable test", TS: now.Unix(), Unflushed: true},
		},
	}
	p.Budget.Limit = DefaultBudget

	out := p.Render()
	if !strings.Contains(out, "1 not yet saved to the vault") {
		t.Errorf("the pack shows work at risk as though it were on disk:\n%s", out)
	}
	if !strings.Contains(out, "logos index") {
		t.Errorf("the pack names the risk without naming the repair:\n%s", out)
	}
}

// And says nothing when there is nothing to say — a standing warning on every
// resume is one nobody reads by the third session.
func TestResumeIsSilentWhenEveryWorkingNoteIsInTheVault(t *testing.T) {
	p := Pack{
		Task:    "resume",
		Working: []session.Note{{Text: "Ran the drop test series.", TS: time.Now().Unix()}},
	}
	p.Budget.Limit = DefaultBudget

	if out := p.Render(); strings.Contains(out, "not yet saved to the vault") {
		t.Errorf("a healthy pack carries a durability warning:\n%s", out)
	}
}
