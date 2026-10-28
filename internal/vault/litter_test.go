package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestATempFileLeftByAKilledWriteIsSweptButALiveOneAndAUsersFileAreNot(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions", "kestrel")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(sessions, ".logos-123456789.tmp")
	live := filepath.Join(sessions, ".logos-987654321.tmp")
	users := filepath.Join(sessions, ".logos-draft.tmp")
	for _, p := range []string{stale, live, users} {
		if err := os.WriteFile(p, []byte("half a checkpoint"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	for _, p := range []string{stale, users} {
		os.Chtimes(p, yesterday, yesterday)
	}

	removed, err := SweepTempLitter(dir, StaleTempAge)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "sessions/kestrel/.logos-123456789.tmp" {
		t.Errorf("removed %v, want only the stale temp file", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale temp file is still there")
	}
	for _, p := range []string{live, users} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was swept: %v", filepath.Base(p), err)
		}
	}
}
