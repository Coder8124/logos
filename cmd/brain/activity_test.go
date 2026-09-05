package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Coder8124/brain/internal/activity"
	"github.com/Coder8124/brain/internal/session"
)

// A plan that fails to save must not vanish without a trace: the hook wrapper
// (plugin/hooks/record.sh) redirects both stdout and stderr to /dev/null on
// every invocation, so the Fprintf this function used to rely on as its "one
// thing 0.8 exists to not lose silently" never reached anyone. The activity
// log just accepted a write and is durable, so a warning row there is the
// fallback that actually survives.
func TestFailedPlanSaveIsReportedInActivityLog(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("BRAIN_VAULT", vault)

	// Force SavePlan to fail: put a plain file where its plans/ directory
	// needs to go, so MkdirPrivate's os.MkdirAll errors on "not a directory".
	planDirPath := filepath.Join(vault, session.CheckpointDir, "brain", session.PlanDir)
	if err := os.MkdirAll(filepath.Dir(planDirPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planDirPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"session_id":"abc123","tool_name":"ExitPlanMode","tool_input":{"plan":"do the thing"}}`)
	restoreStdin := setStdin(t, payload)
	defer restoreStdin()

	if err := recordActivity([]string{"--event", "PostToolUse", "--project", "brain"}); err != nil {
		t.Fatalf("recordActivity returned an error instead of tolerating the failed plan save: %v", err)
	}

	events, err := activity.Read(vault, activity.Query{Kind: activity.KindWarn})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d warning events, want 1 recording the failed plan save", len(events))
	}
}

func setStdin(t *testing.T, data []byte) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	w.Close()

	old := os.Stdin
	os.Stdin = r
	return func() {
		os.Stdin = old
		r.Close()
	}
}
