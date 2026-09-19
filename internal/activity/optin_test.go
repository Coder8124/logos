package activity_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
)

// The log was on the moment the plugin was installed, and nobody was asked. A
// fresh vault must record nothing until someone says yes.
func TestAFreshVaultRecordsNothingUntilSomebodySaysYes(t *testing.T) {
	vault := t.TempDir()

	if activity.Recording(vault) {
		t.Fatal("a vault nobody was asked about is recording")
	}
	if err := activity.Append(vault, activity.Event{TS: 1, Kind: activity.KindPrompt, Summary: "hello"}); err != nil {
		t.Fatal(err)
	}
	events, err := activity.Read(vault, activity.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("an event was recorded before anyone opted in: %+v", events)
	}
}

// The other half of opt-in: an install that has been recording since before the
// question existed keeps its log. Switching it off silently would empty
// `logos activity` for someone who has been relying on it for a fortnight.
func TestAVaultAlreadyRecordingKeepsRecordingWithoutBeingAskedAgain(t *testing.T) {
	vault := t.TempDir()
	dir := filepath.Join(vault, activity.Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A log written by the version that never asked: events, no marker either way.
	line := []byte(`{"ts":1757000000,"kind":"prompt","summary":"from before the question existed"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "2025-09.jsonl"), line, 0o600); err != nil {
		t.Fatal(err)
	}

	if !activity.Recording(vault) {
		t.Fatal("an install that was already recording was switched off without being asked")
	}
}

// Saying yes has to leave a mark of its own. Without one, an empty vault that
// just opted in is indistinguishable from one nobody asked, and `logos activity
// on` reads as a switch that does nothing.
func TestSayingYesOnAnEmptyVaultActuallyTurnsItOn(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	if !activity.Recording(vault) {
		t.Fatal("the log is still off after being turned on")
	}
}

// A month whose events are all past the window is deleted whole. The log said
// it "rolls off under capture retention" while nothing pruned anything, which
// made the most sensitive file in the vault the one that was kept forever.
func TestAMonthPastTheRetentionWindowIsDeletedWhenTheNextOneOpens(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}

	old := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, ts := range []time.Time{old, recent} {
		if err := activity.Append(vault, activity.Event{TS: ts.Unix(), Kind: activity.KindPrompt, Summary: "typed something"}); err != nil {
			t.Fatal(err)
		}
	}
	// Opening June's file is what triggers the sweep.
	if err := activity.Append(vault, activity.Event{TS: now.Unix(), Kind: activity.KindPrompt, Summary: "typed something"}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(vault, activity.Dir)
	if _, err := os.Stat(filepath.Join(dir, "2026-01.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a month five months past the window is still on disk (%v)", err)
	}
	// May ended on the 31st, one day before now: inside the window, kept.
	if _, err := os.Stat(filepath.Join(dir, "2026-05.jsonl")); err != nil {
		t.Errorf("a month still inside the window was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-06.jsonl")); err != nil {
		t.Errorf("the month just written was deleted: %v", err)
	}
}

// Whatever else is beside the log is not ours to delete. The vault is the
// user's directory, and a sweep that guesses is a sweep that eats a file
// somebody put there.
func TestPruningLeavesFilesThatAreNotMonthLogsAlone(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(vault, activity.Dir)
	stray := filepath.Join(dir, "notes-i-kept.jsonl")
	if err := os.WriteFile(stray, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := activity.Append(vault, activity.Event{
		TS: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Unix(), Kind: activity.KindPrompt, Summary: "typed something",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stray); err != nil {
		t.Errorf("a file that is not a month log was deleted: %v", err)
	}
}
