package mcpserver

import (
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

// The Stage 4 claim end to end: an MCP client's remember lands in quarantine,
// stays invisible to recall and list_memories until a human reviews it, and
// only becomes real memory once accepted through the same path `brain review`
// uses. This is the seam that used to be "any agent that calls remember
// mutates the user's vault with no review".
func TestRememberIsQuarantinedByDefault(t *testing.T) {
	c, db, _ := startServer(t)
	handshake(t, c)

	receipt, isErr := c.callText(t, "remember", map[string]any{
		"text": "The user's CFO is Priya.", "kind": "person",
	})
	if isErr {
		t.Fatalf("remember reported error: %s", receipt)
	}
	if !strings.Contains(receipt, "queued") {
		t.Fatalf("remember should say it queued the memory for review, got %q", receipt)
	}

	// Invisible to recall — the whole point of quarantine.
	recall, isErr := c.callText(t, "recall", map[string]any{"query": "who is the CFO"})
	if isErr {
		t.Fatalf("recall errored: %s", recall)
	}
	if strings.Contains(recall, "Priya") {
		t.Fatalf("a quarantined memory was recalled:\n%s", recall)
	}

	// Invisible to list_memories too — not just to semantic recall.
	list, isErr := c.callText(t, "list_memories", nil)
	if isErr {
		t.Fatalf("list_memories errored: %s", list)
	}
	if strings.Contains(list, "Priya") {
		t.Fatalf("a quarantined memory appeared in list_memories:\n%s", list)
	}

	n, err := memory.PendingCount(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("PendingCount = %d, want 1", n)
	}

	pending, err := memory.Pending(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Text, "Priya") {
		t.Fatalf("Pending did not return the queued memory: %+v", pending)
	}

	// Accept is the CLI's review action — do the same thing here directly.
	if err := memory.Accept(db, pending[0].ID); err != nil {
		t.Fatal(err)
	}

	after, isErr := c.callText(t, "recall", map[string]any{"query": "who is the CFO"})
	if isErr {
		t.Fatalf("recall errored: %s", after)
	}
	if !strings.Contains(after, "Priya") {
		t.Fatalf("accepted memory should now be recallable:\n%s", after)
	}
	if left, _ := memory.PendingCount(db); left != 0 {
		t.Errorf("PendingCount after accept = %d, want 0", left)
	}
}

// Rejecting a queued memory discards it for good — it must not resurface on a
// later recall, and it must not still count against the pending total.
func TestRememberRejectedNeverSurfaces(t *testing.T) {
	c, db, _ := startServer(t)
	handshake(t, c)

	if _, isErr := c.callText(t, "remember", map[string]any{
		"text": "The launch date moved to March.", "kind": "context",
	}); isErr {
		t.Fatal("remember reported error")
	}

	pending, err := memory.Pending(db)
	if err != nil || len(pending) != 1 {
		t.Fatalf("want one pending memory, got %v (err %v)", pending, err)
	}
	if err := memory.Reject(db, pending[0].ID); err != nil {
		t.Fatal(err)
	}

	if n, _ := memory.PendingCount(db); n != 0 {
		t.Errorf("PendingCount after reject = %d, want 0", n)
	}
	recall, isErr := c.callText(t, "recall", map[string]any{"query": "launch date"})
	if isErr {
		t.Fatalf("recall errored: %s", recall)
	}
	if strings.Contains(recall, "March") {
		t.Fatalf("a rejected memory was recalled:\n%s", recall)
	}

	// Rejecting again (or accepting) is a mistake to report, not a silent
	// no-op — the row is gone.
	if err := memory.Reject(db, pending[0].ID); err == nil {
		t.Error("rejecting an already-rejected id should error")
	}
}
