package mcpserver

import (
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/usage"
)

// `logos usage` is only as true as what the tools record. A resume is a pack
// handed to an agent and is counted with its size; a before_you_try that
// returned a recorded dead end is counted under the project being worked on,
// even though its search is deliberately vault-wide; one that found nothing
// is not a dead end handed back and is not counted.
func TestResumeAndARuledOutApproachAreCountedInTheUsageLedger(t *testing.T) {
	s := workingSession(t)
	if err := session.Init(s.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(s.DB, s.vault, &session.Checkpoint{
		Project: "shop", Agent: "cursor", Next: "ship the cart fix", TS: time.Now().Unix(),
		Failed: []string{"caching the cart total in redis — stale after a price change"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.dispatch("resume", map[string]any{"project": "shop"}); err != nil {
		t.Fatal(err)
	}
	for _, approach := range []string{
		"cache the cart total in redis",
		"rename the checkout button",
	} {
		if _, err := s.dispatch("before_you_try", map[string]any{"approach": approach, "project": "shop"}); err != nil {
			t.Fatal(err)
		}
	}

	events, bad, err := usage.Read(s.vault)
	if err != nil || bad != 0 {
		t.Fatalf("usage.Read: %v (%d unreadable)", err, bad)
	}
	got := usage.Sum(events, "shop")
	if got.Packs != 1 || got.Sent <= 0 || got.Full < got.Sent {
		t.Errorf("resume was not recorded as one pack with its size: %+v", got)
	}
	if got.DeadEndChecks != 1 || got.Rulings < 1 {
		t.Errorf("want exactly the redis check counted as a dead end handed back, got %+v (events %+v)", got, events)
	}
}
