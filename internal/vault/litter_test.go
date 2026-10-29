package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOnlyALogosTempFileOlderThanAnyStalledWriteIsRemoved(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions", "kestrel")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := ".logos-123456789.tmp"
	stalled := ".logos-555555555.tmp" // ten minutes old: a writer asleep, not dead
	live := ".logos-987654321.tmp"
	users := ".logos-draft.tmp"
	for _, n := range []string{stale, stalled, live, users} {
		if err := os.WriteFile(filepath.Join(sessions, n), []byte("half a checkpoint"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	tenMinutes := time.Now().Add(-10 * time.Minute)
	os.Chtimes(filepath.Join(sessions, stale), yesterday, yesterday)
	os.Chtimes(filepath.Join(sessions, users), yesterday, yesterday)
	os.Chtimes(filepath.Join(sessions, stalled), tenMinutes, tenMinutes)

	var rels []string
	for _, n := range []string{stale, stalled, live, users} {
		rels = append(rels, "sessions/kestrel/"+n)
	}
	removed, err := RemoveTemps(dir, rels)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "sessions/kestrel/"+stale {
		t.Errorf("removed %v, want only the day-old temp file", removed)
	}
	for _, n := range []string{stalled, live, users} {
		if _, err := os.Stat(filepath.Join(sessions, n)); err != nil {
			t.Errorf("%s was removed: %v", n, err)
		}
	}
}

func TestAFailedTempRemovalStillReportsTheOnesItRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	dir := t.TempDir()
	open := filepath.Join(dir, "a")
	locked := filepath.Join(dir, "b")
	for _, d := range []string{open, locked} {
		os.MkdirAll(d, 0o755)
		p := filepath.Join(d, ".logos-1.tmp")
		os.WriteFile(p, nil, 0o600)
		old := time.Now().Add(-24 * time.Hour)
		os.Chtimes(p, old, old)
	}
	os.Chmod(locked, 0o555)
	defer os.Chmod(locked, 0o755)

	removed, err := RemoveTemps(dir, []string{"b/.logos-1.tmp", "a/.logos-1.tmp"})
	if err == nil {
		t.Error("removing a temp file from a read-only directory reported no error")
	}
	if len(removed) != 1 || removed[0] != "a/.logos-1.tmp" {
		t.Errorf("removed %v, want the one it could remove reported alongside the error", removed)
	}
}
