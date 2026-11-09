package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
)

// G1 (plan0-5-5): measure the Stop hook's actual compliance rate before
// building anything else on top of an assumed number nobody had looked at.
func TestCheckpointComplianceRateIsReportedWithItsCounts(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}

	// Session "a": asked, then checkpointed — complies.
	mustAppend(t, vault, activity.Event{TS: 1, Kind: kindCheckpointAsked, Project: "logos", Session: "a"})
	mustAppend(t, vault, activity.Event{TS: 2, Kind: activity.KindTool, Tool: "mcp__plugin_logos_logos__checkpoint", Project: "logos", Session: "a"})

	// Session "b": asked, never checkpointed — does not comply.
	mustAppend(t, vault, activity.Event{TS: 3, Kind: kindCheckpointAsked, Project: "logos", Session: "b"})

	asked, complied, err := complianceCounts(vault, "")
	if err != nil {
		t.Fatal(err)
	}
	if asked != 2 || complied != 1 {
		t.Fatalf("got asked=%d complied=%d, want asked=2 complied=1", asked, complied)
	}

	out := captureStdout(t, func() {
		if err := printComplianceReport(vault, ""); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 of 2") {
		t.Errorf("report did not name the counts, not just a rate:\n%s", out)
	}
}

func TestCheckpointComplianceWithNoAsksSaysSoRatherThanDividingByZero(t *testing.T) {
	vault := t.TempDir()

	out := captureStdout(t, func() {
		if err := printComplianceReport(vault, ""); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no checkpoint asks") {
		t.Errorf("an empty log should say so plainly, got:\n%s", out)
	}
}

func mustAppend(t *testing.T, vault string, e activity.Event) {
	t.Helper()
	if err := activity.Append(vault, e); err != nil {
		t.Fatal(err)
	}
}

// The marker is the newest event when the agent answers the ask, and the
// checkpoint names its conversation from the newest event; with only the cut
// id on it, the answer was filed in a second folder for the same chat.
func TestTheStopHooksAskCarriesTheFullConversationID(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	full := "b8ef6d21-141a-43b2-934d-1de0b746714c"
	stop := activity.Event{Kind: activity.KindTurnEnd, Project: "logos", Session: "b8ef6d21", Agent: "claude-code",
		Extra: map[string]any{"session_id": full}}
	mustAppend(t, vault, activity.Event{TS: 1, Kind: activity.KindTool, Tool: "Edit", Project: "logos", Session: "b8ef6d21", Agent: "claude-code"})
	captureStdout(t, func() { askForCheckpoint(vault, stop, []byte(`{}`)) })

	events, err := activity.Read(vault, activity.Query{Project: "logos"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == kindCheckpointAsked {
			if got, _ := e.Extra["session_id"].(string); got != full {
				t.Errorf("checkpoint-asked carries session_id %q, want %q", got, full)
			}
			return
		}
	}
	t.Fatal("the stop hook did not ask for a checkpoint")
}
