package deadend

import (
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// A failed entry is a ruling: the next agent will not re-try what it names. One
// read out of a transcript by a different agent is that agent's guess at what
// the session concluded, and a wrong guess would retire a route nobody ruled
// out. So before_you_try must not find it.
func TestAFailureInferredFromATranscriptIsNotARuling(t *testing.T) {
	vault := t.TempDir()
	c, err := session.WriteAuto(vault, session.Checkpoint{Project: "shop", Agent: "claude-code",
		State: session.ActivityLogStateFor("lost-1"), Files: []string{"cart.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.InferAuto(vault, c.Slug, session.Inference{By: "cursor",
		Failed: []string{"switching the cart store to redis did not fix the crash (turn 2)"}}); err != nil {
		t.Fatal(err)
	}

	rulings, err := Collect(vault, nil, "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rulings {
		t.Errorf("an inferred failure became a ruling: %+v", r)
	}
}
