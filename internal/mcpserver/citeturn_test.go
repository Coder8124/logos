package mcpserver

import (
	"testing"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/ops"
)

// The review switch accepts only what it can trace to a turn, so with it on a
// proposal has to carry one — and with it off the host's transcript is not
// read at all on a remember, which nobody asked to pay for.
func TestAProposalCitesTheTurnItWasMadeInOnlyWhenTheReviewSwitchIsOn(t *testing.T) {
	srv, vault := testServer(t)
	sess := &Session{Server: srv, clientAgent: "cursor"}
	ts := workedIn("cursor", "shop")
	ts.Path = "/t/cursor-session.jsonl"
	fakeTranscripts(t, ts)

	if got := sess.citeTurn(); got != "" {
		t.Errorf("switch off, and the proposal cited %q", got)
	}
	if err := auto.Set(vault, string(auto.Review), true); err != nil {
		t.Fatal(err)
	}
	if got, want := sess.citeTurn(), ops.TurnRef("cursor", ts.Path, len(ts.Turns)); got != want {
		t.Errorf("cited %q, want %q", got, want)
	}
}
