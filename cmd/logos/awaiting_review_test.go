package main

import (
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

// queueForReview stores a fact the way an MCP client's remember does, into
// quarantine, and leaves a checkpoint so resume has a handoff to print.
func queueForReview(t *testing.T, project, fact string) {
	t.Helper()
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	t.Setenv("LOGOS_RUNTIME", "")
	old := provider.LocalEndpoints
	provider.LocalEndpoints = nil
	t.Cleanup(func() { provider.LocalEndpoints = old })

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: fact, Kind: memory.Fact, Source: "mcp", Project: project, Quarantined: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{Project: project, Next: "refresh the tariff table"}); err != nil {
		t.Fatal(err)
	}
}

// The SessionStart hook injects what `logos resume` prints, and nothing it
// printed said an agent's memory was waiting for the user — so the queue was
// only ever mentioned by doctor, which nobody runs to start work.
func TestResumeSaysMemoriesAreWaitingForReview(t *testing.T) {
	queueForReview(t, "kestrel-one", "Staging DB is on port 5433.")
	out := captureStdout(t, func() {
		if err := runResume([]string{"kestrel-one"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 memory is waiting for your review — logos review") {
		t.Errorf("resume did not mention the queued memory:\n%s", out)
	}
}

// `logos memory add` of a fact an agent already queued said "already knew
// that", while recall still could not find it.
func TestMemoryAddOfAQueuedFactSaysItIsStillQueued(t *testing.T) {
	queueForReview(t, "kestrel-one", "Staging DB is on port 5433.")
	out := captureStdout(t, func() {
		if err := memoryCmd([]string{"add", "Staging DB is on port 5433.", "--project", "kestrel-one"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "already knew") || !strings.Contains(out, "still queued") {
		t.Errorf("memory add should say the fact is still queued for review, got:\n%s", out)
	}
}
