package mcpserver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

// linesUntil collects every line up to and including the one containing needle.
func (c *asyncClient) linesUntil(t *testing.T, needle string, d time.Duration) []string {
	t.Helper()
	var got []string
	deadline := time.After(d)
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				t.Fatalf("the server closed before %s", needle)
			}
			got = append(got, line)
			if strings.Contains(line, needle) {
				return got
			}
		case <-deadline:
			t.Fatalf("no %s within %v", needle, d)
		}
	}
}

func mentions(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// The host timed a call out and told the model it failed, then the reply
// arrived anyway: the model had already acted on the failure.
func TestACancelledCallGetsNoReply(t *testing.T) {
	setEmbedTimeout(t, time.Minute)
	rt, release := stallingRuntime(t)
	c, _ := startAsync(t, withRuntime(t, rt))
	defer release()

	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resume","arguments":{"project":"kestrel-one"}}}`)
	time.Sleep(100 * time.Millisecond)
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2,"reason":"timed out"}}`)
	time.Sleep(50 * time.Millisecond)
	release()
	// tools/list runs on the same worker, so its reply comes after any reply to 2.
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)

	if got := c.linesUntil(t, `"id":3`, 5*time.Second); mentions(got, `"id":2`) {
		t.Errorf("the cancelled call was still answered:\n%s", strings.Join(got, "\n"))
	}
}

// A note the host cancelled while it waited behind a slow call still ran, so
// the model's retry recorded it twice.
func TestACallCancelledBeforeItStartsDoesNotRun(t *testing.T) {
	setEmbedTimeout(t, time.Minute)
	rt, release := stallingRuntime(t)
	c, vault := startAsync(t, withRuntime(t, rt))
	defer release()

	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resume","arguments":{"project":"kestrel-one"}}}`)
	time.Sleep(100 * time.Millisecond)
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"note_progress","arguments":{"project":"kestrel-one","text":"aluminium flexed under load"}}}`)
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	time.Sleep(50 * time.Millisecond)
	release()

	c.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if got := c.linesUntil(t, `"id":4`, 5*time.Second); mentions(got, `"id":3`) {
		t.Errorf("the cancelled note was answered:\n%s", strings.Join(got, "\n"))
	}
	filepath.WalkDir(vault, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "aluminium flexed") {
				t.Errorf("the cancelled note was recorded in %s", path)
			}
		}
		return nil
	})
}

func checkpointFiles(t *testing.T, vault string) []string {
	t.Helper()
	var files []string
	paths, _ := filepath.Glob(filepath.Join(vault, "sessions", "*", "*"))
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && session.IsCheckpointFile(filepath.Base(p)) {
			files = append(files, p)
		}
	}
	return files
}

// A model told "checkpoint failed" checkpoints again, and one session ended up
// with two checkpoint files.
func TestARetriedCheckpointDoesNotWriteASecondFile(t *testing.T) {
	c, vault := startAsync(t)
	args := map[string]any{"project": "kestrel-one", "task": "frame", "next": "order the carbon tube"}

	if _, ok := call(t, c, 2, "checkpoint", args); !ok {
		t.Fatal("the first checkpoint did not answer")
	}
	line, ok := call(t, c, 3, "checkpoint", args)
	if !ok {
		t.Fatal("the retried checkpoint did not answer")
	}
	if n := len(checkpointFiles(t, vault)); n != 1 {
		t.Errorf("a retried identical checkpoint left %d files, want 1", n)
	}
	if !strings.Contains(line, "already saved") {
		t.Errorf("the retry did not say the checkpoint was already saved:\n%s", line)
	}

	// A retry whose checkpoint was since removed is written again rather than
	// answered with a receipt for a file that is gone.
	os.Remove(checkpointFiles(t, vault)[0])
	call(t, c, 5, "checkpoint", args)
	if n := len(checkpointFiles(t, vault)); n != 1 {
		t.Errorf("a retry after the checkpoint was removed left %d files, want 1", n)
	}

	args["next"] = "weigh the frame"
	if _, ok := call(t, c, 4, "checkpoint", args); !ok {
		t.Fatal("a changed checkpoint did not answer")
	}
	if n := len(checkpointFiles(t, vault)); n != 2 {
		t.Errorf("a changed checkpoint left %d files, want 2", n)
	}
}

// The same arguments after a note are a new checkpoint carrying that note, not
// a retry of the last one.
func TestTheSameCheckpointAfterANoteIsWrittenAgain(t *testing.T) {
	c, vault := startAsync(t)
	args := map[string]any{"project": "kestrel-one", "task": "frame", "next": "order the carbon tube"}

	call(t, c, 2, "checkpoint", args)
	call(t, c, 3, "note_progress", map[string]any{"project": "kestrel-one", "text": "the tube arrived bent"})
	if _, ok := call(t, c, 4, "checkpoint", args); !ok {
		t.Fatal("the second checkpoint did not answer")
	}
	if n := len(checkpointFiles(t, vault)); n != 2 {
		t.Errorf("a checkpoint after a note left %d files, want 2", n)
	}
}
