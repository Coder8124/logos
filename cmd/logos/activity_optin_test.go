package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
)

// --yes means "do not stop to ask me", which is a statement about prompts, not
// consent to record every prompt from now on. A provisioning script that runs
// `logos setup --yes` must not come back with the log switched on.
func TestSetupUnderYesLeavesTheActivityLogOffAndSaysHowToTurnItOn(t *testing.T) {
	vault := t.TempDir()

	out := captureStdout(t, func() { offerActivityRecording(vault, true) })

	if activity.Recording(vault) {
		t.Error("--yes turned on a log the user was never asked about")
	}
	if !strings.Contains(out, "logos activity on") {
		t.Errorf("the run never said how to turn it on:\n%s", out)
	}
}

// Setup is re-run often — after a host upgrade, a vault move, another
// `logos mcp install`. A switch that re-offers itself every time is one a user
// eventually flips by accident, in the direction they did not mean.
func TestSetupDoesNotAskAgainOnAVaultThatHasAlreadyAnswered(t *testing.T) {
	for _, answer := range []bool{true, false} {
		vault := t.TempDir()
		if err := activity.SetRecording(vault, answer); err != nil {
			t.Fatal(err)
		}
		was := activity.Recording(vault)

		out := captureStdout(t, func() { offerActivityRecording(vault, true) })

		if strings.TrimSpace(out) != "" {
			t.Errorf("a vault that answered %v was asked again:\n%s", answer, out)
		}
		if activity.Recording(vault) != was {
			t.Errorf("re-running setup changed a vault that had already answered %v", answer)
		}
	}
}

// The other half of opt-in: an install that has been recording since before the
// question existed has effectively answered, and must be left alone rather than
// asked — and answering "no" at a prompt it never meant to see would empty a log
// someone has been relying on.
func TestSetupDoesNotAskAVaultThatWasAlreadyRecording(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	if err := activity.Append(vault, activity.Event{Kind: activity.KindPrompt, Summary: "from before the question existed"}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { offerActivityRecording(vault, true) })

	if strings.TrimSpace(out) != "" {
		t.Errorf("a vault with a log already in it was asked whether to start one:\n%s", out)
	}
	if !activity.Recording(vault) {
		t.Error("setup switched off a log that was already being written")
	}
}
