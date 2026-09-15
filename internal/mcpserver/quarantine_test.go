package mcpserver

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
)

// The Stage 4 claim end to end: an MCP client's remember lands in quarantine,
// stays invisible to recall and list_memories until a human reviews it, and
// only becomes real memory once accepted through the same path `logos review`
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

// Plugin-only and npx-only installs have no logos on PATH, so a receipt
// telling the user to run `logos review` sent them to a command that does not
// exist, and the memory sat in quarantine for good.
func TestTheReviewReceiptNamesTheCommandThisInstallAnswersTo(t *testing.T) {
	s := &Server{Shell: "npx @noeton/logos"}
	if got := s.quarantineReceipt(7, "fact", "everywhere"); !strings.Contains(got, "`npx @noeton/logos review`") {
		t.Errorf("an npx install's receipt must name npx, got %q", got)
	}
	s = &Server{}
	if got := s.quarantineReceipt(7, "fact", "everywhere"); !strings.Contains(got, "`logos review`") {
		t.Errorf("with nothing known about the install the receipt stays `logos review`, got %q", got)
	}
}

// A remembered fact waits in quarantine, and recall one call later said "No
// relevant memories" with no hint that anything was queued. An agent reads that
// as memory being broken or the fact never stored, and the user never learns
// there is something to review. Every read an agent makes has to say so.
func TestRecallResumeAndContextSayMemoriesAreWaitingForReview(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)
	if _, isErr := c.callText(t, "remember", map[string]any{
		"text": "Staging DB is on port 5433.", "project": "kestrel",
	}); isErr {
		t.Fatal("remember reported error")
	}

	for _, read := range []struct {
		tool string
		args map[string]any
	}{
		{"recall", map[string]any{"query": "what port is the staging database on", "project": "kestrel"}},
		{"resume", map[string]any{"project": "kestrel"}},
		{"context", map[string]any{"task": "connect to the staging database", "project": "kestrel"}},
	} {
		out, isErr := c.callText(t, read.tool, read.args)
		if isErr {
			t.Fatalf("%s errored: %s", read.tool, out)
		}
		if !strings.Contains(out, "1 memory is waiting for your review — `logos review`") {
			t.Errorf("%s did not mention the queued memory:\n%s", read.tool, out)
		}
	}
}

// Re-remembering a queued fact said "already knew that — reinforced memory
// #1" while #1 was still in quarantine, and the same session's recall then
// said it was not known. The receipt has to say the fact is still waiting.
func TestRememberingAQueuedFactAgainSaysItIsStillQueued(t *testing.T) {
	c, _, _ := startServer(t)
	handshake(t, c)
	for i := 0; i < 2; i++ {
		receipt, isErr := c.callText(t, "remember", map[string]any{"text": "Staging DB is on port 5433."})
		if isErr {
			t.Fatalf("remember reported error: %s", receipt)
		}
		if i == 0 {
			continue
		}
		if strings.Contains(receipt, "already knew") {
			t.Errorf("a fact still in quarantine was reported as known: %q", receipt)
		}
		if !strings.Contains(receipt, "still queued") || !strings.Contains(receipt, "`logos review`") {
			t.Errorf("the receipt should say the fact is still queued for review, got %q", receipt)
		}
	}
}
