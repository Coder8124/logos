package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// The insights view is a second lens on the same vault the tree pane already
// shows, not a separate store — so a fresh app pointed at a vault with no
// index yet must still see what internal/insight can find, and must say the
// mechanical-tier notice up front (invariant 3), same as the CLI.
func TestInsightsReportsTheMechanicalNoticeAndARecurringBlocker(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	ix, err := a.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "first",
		Blockers: []string{"the vendor has not shipped the connector firmware"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project:  "kestrel-one",
		Next:     "second",
		Blockers: []string{"still waiting on the connector firmware from the vendor"},
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	view, err := a.Insights("kestrel-one")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Degraded, "mechanical") {
		t.Fatalf("expected the mechanical-tier notice, got %q", view.Degraded)
	}
	var found *Insight
	for i := range view.Insights {
		if view.Insights[i].Kind == "recurring-blocker" {
			found = &view.Insights[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a recurring-blocker insight, got %v", view.Insights)
	}
	if len(found.Sources) != 2 {
		t.Fatalf("expected the insight to cite both checkpoints, got %v", found.Sources)
	}
}

// An insight the citation filter refused must not vanish without a trace —
// the view carries the drop count so the panel can say "N found, M dropped"
// instead of silently shrinking the list.
func TestInsightsOnAnEmptyVaultReportsZeroWithNoDrops(t *testing.T) {
	dir := t.TempDir()
	a := NewApp(dir)

	view, err := a.Insights("")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Insights) != 0 || len(view.Drops) != 0 {
		t.Fatalf("expected an empty vault to report nothing, got %v / %v", view.Insights, view.Drops)
	}
}
