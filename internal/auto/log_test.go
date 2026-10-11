package auto

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAMangledLogLineDoesNotHideTheRestOfTheLog(t *testing.T) {
	vault := t.TempDir()
	if err := Record(vault, Entry{Switch: Checkpoint, Project: "shop", N: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(LogPath(vault), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("- yesterday-ish checkpoint n=lots\n")
	f.Close()
	if err := Record(vault, Entry{Switch: Checkpoint, Project: "shop", N: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := Entries(vault, "shop", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("read %d entries around a bad line, want 2", len(got))
	}
}

func TestTheLogForOneProjectLeavesOutAnothersButNotTheVaultWideOnes(t *testing.T) {
	vault := t.TempDir()
	for _, e := range []Entry{{Switch: Checkpoint, Project: "shop", N: 1}, {Switch: Checkpoint, Project: "other", N: 1}, {Switch: "index", N: 1}} {
		if err := Record(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	mine, _ := Entries(vault, "shop", time.Now().Add(-time.Hour))
	all, _ := Entries(vault, "", time.Now().Add(-time.Hour))
	if len(mine) != 2 || len(all) != 3 {
		t.Errorf("shop read %d entries (want 2), every project %d (want 3)", len(mine), len(all))
	}
}

// Two failures for two reasons are two things to fix; a count would hide one.
func TestEachFailureIsSaidWithItsReasonNotCounted(t *testing.T) {
	got := Summary([]Entry{
		{Switch: Checkpoint, N: 1}, {Switch: Checkpoint, N: 1},
		{Switch: Checkpoint, Failed: "disk full"}, {Switch: Checkpoint, Failed: "vault locked"},
	})
	for _, want := range []string{"checkpointed 2 sessions", "checkpoint failed (disk full)", "checkpoint failed (vault locked)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q is missing %q", got, want)
		}
	}
	if Summary(nil) != "" {
		t.Error("an empty log is not silent")
	}
}
