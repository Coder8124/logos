package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// openStdin hands the command a pipe whose writer stays open, the way an agent
// host's shell tool or a background job does, and returns the writer.
func openStdin(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; w.Close(); r.Close() })
	return w
}

// runCheckpointWithin fails the test instead of hanging the suite when the
// checkpoint never returns.
func runCheckpointWithin(t *testing.T, limit time.Duration, args []string) string {
	t.Helper()
	var out string
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		out = captureStdout(t, func() { err = runCheckpoint(args) })
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("logos checkpoint %v still running after %v with stdin open and silent", args, limit)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An agent's shell tool can leave stdin an open pipe that never sends a byte.
// The checkpoint the flags already describe must still be written, and the
// receipt must say stdin was passed over rather than drop it silently.
func TestACheckpointWithFlagsIsWrittenWhenStdinIsOpenAndSilent(t *testing.T) {
	standIn(t, "shop")
	openStdin(t)
	old := stdinGrace
	stdinGrace = 100 * time.Millisecond
	t.Cleanup(func() { stdinGrace = old })

	out := runCheckpointWithin(t, 10*time.Second, []string{"shop", "--task", "fix the cache", "--next", "ship it"})

	c, err := session.Latest(vaultPath(), "shop")
	if err != nil || c == nil || c.Task != "fix the cache" {
		t.Fatalf("the checkpoint from the flags was not written: %+v, %v", c, err)
	}
	if !strings.Contains(out, "stdin") {
		t.Errorf("the receipt does not say stdin was passed over:\n%s", out)
	}
}

// The grace period is for a pipe that never speaks, not a slow one: input
// that starts arriving inside it is read to the end and merged as before.
func TestPipedInputThatArrivesWithinTheGraceIsStillMerged(t *testing.T) {
	standIn(t, "shop")
	w := openStdin(t)
	old := stdinGrace
	stdinGrace = 5 * time.Second
	t.Cleanup(func() { stdinGrace = old })
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.WriteString("## Next\nship it\n")
		time.Sleep(50 * time.Millisecond)
		w.WriteString("\n## Didn't work\n- retrying hid the race\n")
		w.Close()
	}()

	out := runCheckpointWithin(t, 10*time.Second, []string{"shop", "--task", "fix the cache"})

	c, err := session.Latest(vaultPath(), "shop")
	if err != nil || c == nil {
		t.Fatalf("no checkpoint: %v", err)
	}
	if c.Next != "ship it" || len(c.Failed) != 1 {
		t.Errorf("piped sections were not merged: next %q, failed %q", c.Next, c.Failed)
	}
	if strings.Contains(out, "stdin") {
		t.Errorf("the receipt says stdin was passed over when it was read:\n%s", out)
	}
}
