package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
)

// #88: the log had no off switch other than uninstalling the plugin.
func TestActivityOffAndOnAreCommandsAndSayWhatTheyDid(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	out := captureStdout(t, func() {
		if err := runActivity([]string{"off"}); err != nil {
			t.Fatal(err)
		}
	})
	if activity.Recording(vault) {
		t.Error("`logos activity off` did not stop the log")
	}
	if !strings.Contains(out, "off") || !strings.Contains(out, "activity on") {
		t.Errorf("nothing said what happened or how to undo it:\n%s", out)
	}

	out = captureStdout(t, func() {
		if err := runActivity([]string{"on"}); err != nil {
			t.Fatal(err)
		}
	})
	if !activity.Recording(vault) {
		t.Error("`logos activity on` did not resume the log")
	}
	if !strings.Contains(out, "on") {
		t.Errorf("nothing said the log is recording again:\n%s", out)
	}
}

// Invariant 3 the other way round: a log that is off must say so where someone
// looks for it, not read as a vault with nothing in it.
func TestListingActivityWithTheLogOffSaysItIsOff(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	if err := activity.SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runActivity(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "off") {
		t.Errorf("an empty log that is switched off reads as a log with nothing in it:\n%s", out)
	}
}

// The hook asks for this every session start and injects whatever comes back,
// so an empty answer has to be genuinely empty: a notice repeated every session
// is one the model stops relaying and the user stops reading.
func TestTheActivityNoticeIsPrintedOnceAndThenNothing(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	first := captureStdout(t, func() {
		if err := runActivity([]string{"notice"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(first, "logos activity off") {
		t.Errorf("the first session was not told what the log records or how to stop it:\n%s", first)
	}

	again := captureStdout(t, func() {
		if err := runActivity([]string{"notice"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(again) != "" {
		t.Errorf("the notice is repeated every session:\n%s", again)
	}
}
