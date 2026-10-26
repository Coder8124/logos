package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
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

// A procedure's evidence is earned in the ledger, not rewritten in its text:
// before_you_try reads how often a procedure was shown and proven from it, so
// one recorded as once whose verify has passed since reads as verified, and
// each showing is itself recorded for the next count.
func TestBeforeYouTryReadsAProceduresEvidenceFromTheLedger(t *testing.T) {
	s := workingSession(t)
	if err := memory.Init(s.DB); err != nil {
		t.Fatal(err)
	}
	text := "route: run migrations with the lock flag | trap: two deploys at once corrupt the schema table | verify: make migrate-check | evidence: once"
	if _, err := memory.Store(s.DB, nil, "", &memory.Memory{Text: text, Kind: memory.Procedure, Source: "mcp", Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	ref := usage.Ref(text)
	for _, kind := range []string{usage.KindProcedure, usage.KindProven} {
		if err := usage.Record(s.vault, usage.Event{Kind: kind, Project: "shop", Session: "earlier", Refs: []string{ref}}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := s.dispatch("before_you_try", map[string]any{"approach": "run migrations with the lock flag", "project": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "verified (recorded once; its verify has passed since)") ||
		!strings.Contains(out, "shown 1 time before · its verify passed after it in 1 session") {
		t.Errorf("the ledger's evidence is not on the procedure:\n%s", out)
	}
	events, _, err := usage.Read(s.vault)
	if err != nil {
		t.Fatal(err)
	}
	if got := usage.Tallies(events)[ref]; got.Shown != 2 {
		t.Errorf("this showing was not recorded: %+v", got)
	}

	// A line the ledger could not parse may have been one of these showings, so
	// counts read past it are said to be possibly low rather than shown as exact.
	files, _ := filepath.Glob(filepath.Join(s.vault, usage.Dir, "*.jsonl"))
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not json\n")
	f.Close()
	out, err = s.dispatch("before_you_try", map[string]any{"approach": "run migrations with the lock flag", "project": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 unreadable usage ledger line(s) were skipped") {
		t.Errorf("counts read past an unreadable ledger line were not marked:\n%s", out)
	}
}
