package activity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/activity"
)

// The log records prompts and tool calls whether or not anybody asked for it,
// and until this the only way to stop it was to uninstall the plugin. A record
// nobody can turn off is surveillance however good the redaction is.
func TestTurningTheLogOffStopsItRecording(t *testing.T) {
	vault := t.TempDir()

	if err := activity.SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	if activity.Recording(vault) {
		t.Fatal("the log still reports itself on after being turned off")
	}
	if err := activity.Append(vault, activity.Event{TS: 1, Kind: activity.KindPrompt, Summary: "hello"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	events, err := activity.Read(vault, activity.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("an event was recorded with the log off: %+v", events)
	}

	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	if err := activity.Append(vault, activity.Event{TS: 2, Kind: activity.KindPrompt, Summary: "hello"}); err != nil {
		t.Fatal(err)
	}
	events, err = activity.Read(vault, activity.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("turning it back on did not resume recording: %+v", events)
	}
}

// The setting is in the vault, not in .logos/: deleting the index is documented
// as lossless, and "the log I turned off came back on" is a loss.
func TestTheRecordingSettingSurvivesDeletingTheIndex(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(vault, ".logos")); err != nil {
		t.Fatal(err)
	}
	if activity.Recording(vault) {
		t.Error("deleting the index turned the log back on")
	}
}

// Nobody consented to the log: the plugin install that starts recording is the
// same one that installs the hooks, and the user is told about neither. So the
// first session after an install has to say it, once — silence here is the
// difference between a feature and surveillance.
func TestTheLogDisclosesItselfOnceAndThenStopsSayingIt(t *testing.T) {
	vault := t.TempDir()
	// There is only something to disclose once the log is actually running.
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}

	first, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("a fresh vault never told the user its prompts are being logged")
	}
	if !strings.Contains(first, "logos activity off") {
		t.Errorf("the disclosure does not say how to stop it:\n%s", first)
	}
	if !strings.Contains(first, filepath.Join(vault, activity.Dir)) {
		t.Errorf("the disclosure does not name where the log lands:\n%s", first)
	}

	if err := activity.MarkDisclosed(vault); err != nil {
		t.Fatal(err)
	}
	again, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if again != "" {
		t.Errorf("the disclosure repeats every session:\n%s", again)
	}
}

// Nothing is being recorded, so there is nothing to disclose; saying it anyway
// would teach the user to ignore the line in the sessions that matter.
func TestATurnedOffLogHasNothingToDisclose(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	notice, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Errorf("a log that is off announced that it is recording:\n%s", notice)
	}
}

// A disclosure marked said before it has been said is a disclosure the user can
// lose to one crashed session and never be offered again — and unlike the
// update notice, "never saw it" is the whole failure here rather than a
// cosmetic one. Asking is not saying: only MarkDisclosed ends it.
func TestAskingForTheDisclosureDoesNotCountAsHavingSaidIt(t *testing.T) {
	vault := t.TempDir()
	// There is only something to disclose once the log is actually running.
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}

	first, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("a fresh vault had nothing to disclose")
	}

	again, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("the notice was marked said by the act of asking for it, so a session that crashed before printing it would never offer it again:\n%q", again)
	}

	if err := activity.MarkDisclosed(vault); err != nil {
		t.Fatal(err)
	}
	after, err := activity.Disclose(vault)
	if err != nil {
		t.Fatal(err)
	}
	if after != "" {
		t.Errorf("the notice is still offered after it was marked said:\n%q", after)
	}
}
