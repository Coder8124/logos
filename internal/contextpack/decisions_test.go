package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

func renderAfter(t *testing.T, cps ...*session.Checkpoint) string {
	t.Helper()
	ix := seedVault(t)
	now := time.Now()
	for i, c := range cps {
		c.Project = "kestrel-one"
		c.TS = now.Add(time.Duration(i-len(cps)) * time.Minute).Unix()
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "should we switch sessions to JWT tokens", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return p.Render()
}

const cookies = "Sessions use signed cookies, not JWTs, because we need server-side revocation"

// A decision recorded in one checkpoint vanished from every pack the moment
// another checkpoint landed without restating it, while a ruled-out approach
// from the same checkpoint was carried forward. The next agent re-opened the
// settled question.
func TestADecisionIsStillHandedOverAfterANewerCheckpoint(t *testing.T) {
	out := renderAfter(t,
		&session.Checkpoint{Agent: "claude", Task: "session auth", Decisions: []string{cookies}},
		&session.Checkpoint{Agent: "cursor", Task: "fix CSS on the landing page", Next: "check mobile"},
	)
	if !strings.Contains(out, "**Decided earlier in this project:**") ||
		!strings.Contains(out, "(claude, ") || !strings.Contains(out, "signed cookies, not JWTs") {
		t.Errorf("the earlier decision is not carried forward, attributed:\n%s", out)
	}
}

// Restated decisions are one decision: the latest checkpoint already prints it
// under Decided, and two earlier checkpoints saying it again add nothing.
func TestAnEarlierDecisionIsListedOnceAndNotWhenTheLatestRestatesIt(t *testing.T) {
	out := renderAfter(t,
		&session.Checkpoint{Agent: "claude", Decisions: []string{cookies}},
		&session.Checkpoint{Agent: "codex", Decisions: []string{cookies, "Deploys go through the staging cluster first"}},
		&session.Checkpoint{Agent: "cursor", Next: "check mobile"},
	)
	if n := strings.Count(out, "signed cookies, not JWTs"); n != 1 {
		t.Errorf("the decision appears %d times, want once:\n%s", n, out)
	}
	restated := renderAfter(t,
		&session.Checkpoint{Agent: "claude", Decisions: []string{cookies}},
		&session.Checkpoint{Agent: "cursor", Decisions: []string{cookies}},
	)
	if strings.Contains(restated, "Decided earlier in this project") {
		t.Errorf("a decision the latest checkpoint restates is listed again as earlier:\n%s", restated)
	}
}

// A later decision on the same question replaces the earlier one. Handing the
// agent both is handing it a choice that was already made the other way.
func TestAnEarlierDecisionALaterOneOvertookIsNotHandedOver(t *testing.T) {
	out := renderAfter(t,
		&session.Checkpoint{Agent: "claude", Decisions: []string{cookies}},
		&session.Checkpoint{Agent: "codex", Decisions: []string{"Sessions move to JWTs; signed cookies are dropped because revocation is handled by a denylist"}},
		&session.Checkpoint{Agent: "cursor", Next: "check mobile"},
	)
	if strings.Contains(out, "signed cookies, not JWTs") {
		t.Errorf("an overtaken decision is still handed over:\n%s", out)
	}
	if !strings.Contains(out, "Sessions move to JWTs") {
		t.Errorf("the decision that overtook it is missing:\n%s", out)
	}
}

// A decision promoted to project memory is the same decision the checkpoints
// recorded: resume hands it over once, as project memory with its count, not
// again from each checkpoint and again among the memories.
func TestAPromotedDecisionIsHandedOverOnceWithItsCheckpointCount(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	cps := []*session.Checkpoint{
		{Agent: "claude", Decisions: []string{cookies}},
		{Agent: "codex", Decisions: []string{cookies}},
		{Agent: "claude", Decisions: []string{cookies}},
		{Agent: "cursor", Task: "fix CSS on the landing page", Next: "check mobile"},
	}
	for i, c := range cps {
		c.Project = "kestrel-one"
		c.TS = now.Add(time.Duration(i-len(cps)) * time.Minute).Unix()
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: "Decision: " + cookies, Kind: memory.Context,
		Project: "kestrel-one", Source: memory.SourceCheckpoints}); err != nil {
		t.Fatal(err)
	}
	p, err := Build(ix, nil, "", Request{Task: "should we switch sessions to JWT tokens", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "(project memory, from 3 checkpoints) Decision: "+cookies) {
		t.Errorf("the promoted decision is not handed over with its count:\n%s", out)
	}
	if n := strings.Count(out, "signed cookies, not JWTs"); n != 1 {
		t.Errorf("the decision is handed over %d times, want once:\n%s", n, out)
	}
}
