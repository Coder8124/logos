package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// The CLI wiring for `brain sessions <project> --close <id>`: end to end
// against a real vault, not just the internal/session package the command
// delegates to.
func TestSessionsCloseFlagResolvesAnAbandonedSession(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * session.AbandonAfter).Unix()
	n, err := session.AddNoteAt(ix.DB, "demo", "claude", "died mid-task", old)
	if err != nil {
		t.Fatal(err)
	}
	ix.Close()

	if err := runSessionLog([]string{"demo", "--close", n.Session}); err != nil {
		t.Fatalf("--close: %v", err)
	}

	hist, err := session.History(vault, "demo", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || !hist[0].AutoClosed {
		t.Fatalf("expected one AutoClosed checkpoint on disk, got: %+v", hist)
	}

	// A second --close on the same id must refuse rather than silently no-op —
	// there is nothing left to close.
	if err := runSessionLog([]string{"demo", "--close", n.Session}); err == nil {
		t.Fatal("closing an already-closed session should error")
	}
	if err := runSessionLog([]string{"demo", "--close", "20260101-000000-nobody"}); err == nil {
		t.Fatal("closing an unknown session id should error")
	}
}

func TestSessionsCloseFlagRefusesASessionStillWithinTheAbandonmentWindow(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	n, err := session.AddNote(ix.DB, "demo", "claude", "just started this")
	if err != nil {
		t.Fatal(err)
	}
	ix.Close()

	err = runSessionLog([]string{"demo", "--close", n.Session})
	if err == nil || !strings.Contains(err.Error(), "not safe to auto-close") {
		t.Errorf("closing a fresh session should refuse with the abandonment-window reason, got: %v", err)
	}
}
