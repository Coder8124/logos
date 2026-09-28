package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// The reasoning behind a task used to live only in the transcript: a resume
// said what the last agent was doing and never why, so the next one reread
// the conversation or guessed. Intent is written once, and every later
// checkpoint of the same task — an auto record included, which carries no
// reasoning of its own — hands it over.
func TestTheWhyBehindATaskCarriesForwardToLaterCheckpointsOfIt(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	for _, c := range []*session.Checkpoint{
		{Project: "kestrel-one", Agent: "claude", Task: "cut the BOM to $118",
			Intent: "the retailer contract fixes the launch price, so margin only exists at $118",
			Next:   "re-quote the display stack", TS: now.Add(-2 * time.Minute).Unix()},
		{Project: "kestrel-one", Agent: "codex", Task: "cut the BOM to $118",
			Next: "get the single-mic quote", TS: now.Add(-time.Minute).Unix()},
	} {
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	doing := strings.Index(out, "**They were doing:**")
	why := strings.Index(out, "**Why:** the retailer contract fixes the launch price")
	if doing < 0 || why < doing {
		t.Fatalf("the task's reason is not handed over beneath it:\n%s", out)
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out[why:], "earlier checkpoint") {
		t.Errorf("a carried reason does not say it came from an earlier checkpoint:\n%s", out)
	}
}

// A different task's reason is not this task's reason.
func TestAnotherTasksWhyIsNotHandedOver(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	for _, c := range []*session.Checkpoint{
		{Project: "kestrel-one", Agent: "claude", Task: "migrate payments to stripe v3",
			Intent: "v2 webhooks stop being delivered in March",
			Next:   "finish webhook signature check", TS: now.Add(-2 * time.Minute).Unix()},
		{Project: "kestrel-one", Agent: "codex", Task: "fix CSS on the landing page",
			Next: "check mobile", TS: now.Add(-time.Minute).Unix()},
	} {
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); strings.Contains(out, "**Why:**") {
		t.Errorf("the CSS task was handed the payments migration's reason:\n%s", out)
	}
}

// An auto record's task is the user's first prompt, never the agent's wording,
// so it cannot match by task. It continues the agent-written checkpoint before
// it, and inherits that checkpoint's reason — said to come from there.
func TestAnAutoRecordInheritsTheWhyOfTheCheckpointItFollows(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude", Task: "cut the BOM to $118",
		Intent: "the retailer contract fixes the launch price",
		Next:   "re-quote the display stack",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.WriteAuto(ix.Vault, session.Checkpoint{
		Project: "kestrel-one", Agent: "claude", Task: "keep going on the bom",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); !strings.Contains(out, "**Why:** the retailer contract fixes the launch price") {
		t.Errorf("an auto record lost the reason of the work it continues:\n%s", out)
	}
}

// Resume reads a handful of checkpoints for their dead ends. The reason for a
// long task was written further back than that, and must still arrive.
func TestTheWhyOfALongTaskSurvivesMoreCheckpointsThanResumeReads(t *testing.T) {
	ix := seedVault(t)
	now := time.Now()
	for i := 8; i >= 1; i-- {
		c := &session.Checkpoint{Project: "kestrel-one", Agent: "claude", Task: "cut the BOM to $118",
			Next: "keep going", TS: now.Add(-time.Duration(i) * time.Minute).Unix()}
		if i == 8 {
			c.Intent = "the retailer contract fixes the launch price"
		}
		if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if out := p.Render(); !strings.Contains(out, "**Why:** the retailer contract fixes the launch price") {
		t.Errorf("the reason fell out of resume after eight saves of the same task:\n%s", out)
	}
}
