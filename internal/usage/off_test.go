package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// Everything Logos writes on its own gets a switch. The ledger holds counts,
// not prompts, but a record the user cannot stop is still one they did not
// agree to.
func TestTurningTheLedgerOffStopsItRecordingAndOnResumesIt(t *testing.T) {
	vault := t.TempDir()
	if err := SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	if Recording(vault) {
		t.Fatal("the ledger still reports itself on after being turned off")
	}
	if err := Record(vault, Event{Kind: KindPack, Project: "p", Sent: 10, Full: 20}); err != nil {
		t.Fatal(err)
	}
	if events, _, _ := Read(vault); len(events) != 0 {
		t.Errorf("an event was recorded with the ledger off: %+v", events)
	}

	if err := SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	if err := Record(vault, Event{Kind: KindPack, Project: "p", Sent: 10, Full: 20}); err != nil {
		t.Fatal(err)
	}
	if events, _, _ := Read(vault); len(events) != 1 {
		t.Errorf("turning it back on did not resume recording: %+v", events)
	}
}

// The setting lives in the vault, not in .logos/: deleting the index is
// documented as lossless, and "the ledger I turned off came back on" is a loss.
func TestTheLedgerSettingSurvivesDeletingTheIndex(t *testing.T) {
	vault := t.TempDir()
	if err := SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(vault, ".logos")); err != nil {
		t.Fatal(err)
	}
	if Recording(vault) {
		t.Error("deleting the index turned the ledger back on")
	}
}

// LOGOS_USAGE=off is the per-process switch, for a CI run or a demo that
// should not add to the totals, without touching the vault's setting.
func TestLogosUsageOffInTheEnvironmentStopsRecording(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_USAGE", "off")
	if err := Record(vault, Event{Kind: KindDeadEnd, Project: "p", Rulings: 1}); err != nil {
		t.Fatal(err)
	}
	if events, _, _ := Read(vault); len(events) != 0 {
		t.Errorf("recorded with LOGOS_USAGE=off: %+v", events)
	}
}
