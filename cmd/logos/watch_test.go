package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// lockedBuffer is watch's terminal: the test reads it while watch writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func waitFor(t *testing.T, out *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("watch never printed %q:\n%s", want, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// watch is a tail, not a listing: what was in the vault before it started is
// what `logos activity` is for, and replaying it would bury the moment the demo
// exists to show — a capture, a ruling handed back, a memory saved, each
// printed as it happens.
func TestWatchPrintsACaptureAsItHappens(t *testing.T) {
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	if err := activity.Append(vault, activity.Event{Kind: activity.KindTool, Tool: "Edit", Summary: "edited old.go"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- watchVault(ctx, vault, out, 10*time.Millisecond) }()
	waitFor(t, out, "Watching")

	if err := activity.Append(vault, activity.Event{Kind: activity.KindTool, Tool: "Edit", Project: "shop",
		Summary: "edited checkout.go", Private: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "edited checkout.go")
	waitFor(t, out, "Kept out 1 <private> span(s)")

	if err := usagepkg.Record(vault, usagepkg.Event{Kind: usagepkg.KindDeadEnd, Project: "shop", Via: "mcp:before_you_try", Rulings: 2}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "2 ruled-out approaches handed back")

	// A memory reaches the timeline by appending to memories/log.md, the way
	// every store does; written here directly so the test does not need an index.
	log := filepath.Join(vault, "memories", "log.md")
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		t.Fatal(err)
	}
	line := "- invoices round half-even <!-- logos ts=2026-10-11T10:00:00Z id=7 ev=created project=shop -->\n"
	if err := os.WriteFile(log, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "#7 created")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch ended with %v", err)
	}
	if strings.Contains(out.String(), "old.go") {
		t.Errorf("watch replayed an event from before it started:\n%s", out.String())
	}
}

// A watch on a vault that records no captures would sit silent forever and
// read as a broken command; it says why nothing will show.
func TestWatchSaysWhenCapturesAreNotBeingRecorded(t *testing.T) {
	vault := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- watchVault(ctx, vault, out, 10*time.Millisecond) }()
	waitFor(t, out, "Watching")
	cancel()
	<-done
	if !strings.Contains(out.String(), "logos activity on") {
		t.Errorf("watch did not say captures are off:\n%s", out.String())
	}
}
