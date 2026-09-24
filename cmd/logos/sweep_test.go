package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/transcript"
)

// The SessionStart hook runs this command, so this is the path every Claude
// Code session start takes: a session that ended without a checkpoint, and
// without a shutdown path that got to run, is recorded and announced (#134).
func TestCLIResumeRecordsAndAnnouncesASessionThatEndedWithoutACheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	ended := time.Now().Add(-2 * time.Hour).Unix()
	was := ingest.RecentTranscripts
	ingest.RecentTranscripts = func(string, time.Time, time.Time, func(string, time.Time) bool) ([]*transcript.Session, []string) {
		return []*transcript.Session{{
			Harness: "codex", ID: "lost-1", Project: "shop",
			Started: ended - 1200, Ended: ended,
			Turns: []transcript.Turn{
				{Role: "user", Text: "fix the checkout crash when cart is empty"},
				{Role: "tool", Tool: "edit_file", Input: "cart.go"},
			},
		}}, nil
	}
	t.Cleanup(func() { ingest.RecentTranscripts = was })

	out := captureStdout(t, func() {
		if err := runResume([]string{"shop"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Recorded 1 earlier session that ended without a checkpoint") {
		t.Errorf("resume did not announce the session it recorded:\n%s", out)
	}
	if !strings.Contains(out, "not written by an agent, unverified") {
		t.Errorf("the recorded session is not in the handoff, labelled auto:\n%s", out)
	}
}
