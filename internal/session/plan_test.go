package session

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanMarkdownRoundTrips(t *testing.T) {
	want := Plan{
		Project: "kestrel-one",
		Agent:   "claude",
		Text:    "1. Cut the BOM to the $118 target.\n2. Re-quote the waveguide.",
		TS:      1755172800,
	}

	got := ParsePlan(want.Markdown())
	got.Slug = "" // Slug is set by SavePlan/ListPlans, not carried in the note itself.

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip lost information:\n got %+v\nwant %+v", got, want)
	}
}

// planFilename must never satisfy IsCheckpointFile's "8 digits then a dash"
// test — that test is how health.latestCheckpoint and session.History both
// decide a file in a session directory is a real checkpoint, and a plan file
// that passed it would be misread as one, the same bug this repository
// already fixed once for uncommitted.md.
func TestPlanFilenameNeverMatchesACheckpoint(t *testing.T) {
	name := planFilename(1755172800, "claude")
	if IsCheckpointFile(name) {
		t.Errorf("plan filename %q satisfies IsCheckpointFile — it will be misread as a checkpoint", name)
	}
}

func TestSavedPlanIsListedByListPlans(t *testing.T) {
	vaultDir := t.TempDir()

	slug, err := SavePlan(vaultDir, Plan{
		Project: "brain",
		Agent:   "claude",
		Text:    "Persist plan-mode plans into the vault.",
		TS:      1755172800,
	})
	if err != nil {
		t.Fatal(err)
	}
	if slug == "" {
		t.Fatal("SavePlan returned an empty slug")
	}

	got, err := ListPlans(vaultDir, "brain")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d plans, want 1", len(got))
	}
	if got[0].Text != "Persist plan-mode plans into the vault." {
		t.Errorf("got text %q", got[0].Text)
	}
	if got[0].Slug != slug {
		t.Errorf("got slug %q, want %q", got[0].Slug, slug)
	}
}

// A plan lives under sessions/<project>/plans/, a subdirectory of the same
// directory History reads. History's os.ReadDir is non-recursive, so the
// plans/ entry must be skipped as a directory rather than misparsed as a
// checkpoint file.
func TestPlansDoNotAppearInCheckpointHistory(t *testing.T) {
	vaultDir := t.TempDir()

	if _, err := SavePlan(vaultDir, Plan{
		Project: "brain",
		Agent:   "claude",
		Text:    "a plan",
		TS:      1755172800,
	}); err != nil {
		t.Fatal(err)
	}

	real := Checkpoint{Session: "20260814-143207-claude", Project: "brain", Agent: "claude", Task: "did a thing", TS: 1755172800}
	dir := filepath.Join(vaultDir, CheckpointDir, "brain")
	if err := os.WriteFile(filepath.Join(dir, real.Session+".md"), []byte(real.Markdown("")), 0o600); err != nil {
		t.Fatal(err)
	}

	hist, err := History(vaultDir, "brain", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("got %d checkpoints in history, want 1 (the plans/ subdirectory must be skipped): %+v", len(hist), hist)
	}
}
