package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/dream"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/secretary"
)

// deleteLineBy removes every line of path that contains needle, the way a
// person deletes a bullet in their editor.
func deleteLineBy(t *testing.T, path, needle string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, needle) {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(strings.Split(string(raw), "\n")) {
		t.Fatalf("%s has no line containing %q:\n%s", path, needle, raw)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// #240: listing read the cache and never the file, so a loop deleted from
// loops.md was still listed — at the one moment a person checks whether their
// edit took.
func TestALoopDeletedFromTheFileIsNotListed(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")

	for _, text := range []string{"send the optics quote", "renew the lab badge"} {
		if err := commitmentCmd([]string{"add", text}); err != nil {
			t.Fatal(err)
		}
	}
	deleteLineBy(t, secretary.LoopsPath(vaultDir), "send the optics quote")

	out := captureStdout(t, func() {
		if err := commitmentCmd(nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "send the optics quote") {
		t.Errorf("`logos loop` still lists a loop deleted from loops.md:\n%s", out)
	}
	if !strings.Contains(out, "renew the lab badge") {
		t.Errorf("`logos loop` lost the loop that was not deleted:\n%s", out)
	}
	if !strings.Contains(out, "1 loop forgotten") {
		t.Errorf("`logos loop` adopted the deletion without saying so:\n%s", out)
	}
}

// #241: a write adopts the file before it rewrites it, and that adoption was
// silent — `loop add` printed only "tracked" while it also forgot a loop.
func TestAddingALoopAfterAHandDeletionSaysTheDeletedLoopWasForgotten(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")

	if err := commitmentCmd([]string{"add", "send the optics quote"}); err != nil {
		t.Fatal(err)
	}
	deleteLineBy(t, secretary.LoopsPath(vaultDir), "send the optics quote")

	out := captureStdout(t, func() {
		if err := commitmentCmd([]string{"add", "renew the lab badge"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 loop forgotten") || !strings.Contains(out, "tracked") {
		t.Errorf("`loop add` after a hand deletion should say one loop was forgotten, then tracked:\n%s", out)
	}
}

// #240 for the review queue: a proposal deleted from pending.md is a
// rejection, and `logos review` offered it for review anyway.
func TestAProposalDeletedFromTheQueueFileIsNotOfferedForReview(t *testing.T) {
	queueForReview(t, "kestrel-one", "Staging DB is on port 5433.")
	vaultDir := os.Getenv("LOGOS_VAULT")
	deleteLineBy(t, filepath.Join(vaultDir, memory.Dir, memory.PendingFile), "5433")

	out := captureStdout(t, func() {
		if err := runReview(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "review queue is empty") {
		t.Errorf("`logos review` offered a proposal deleted from the queue file:\n%s", out)
	}
	if !strings.Contains(out, "1 proposal rejected") {
		t.Errorf("`logos review` adopted the deletion without saying so:\n%s", out)
	}
}

// #240 for dreamed insights: the same, for `logos dream review`.
func TestAnInsightDeletedFromTheQueueFileIsNotOfferedForReview(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := dream.InitQueue(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := dream.Enqueue(ix.DB, &dream.Insight{
		Kind: dream.Connection, Text: "the badge renewal and the optics quote share a vendor",
		EndpointA: 1, EndpointB: 2, Conf: 0.7, Model: "test",
	}); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	deleteLineBy(t, dream.InsightsPath(vaultDir), "share a vendor")

	out := captureStdout(t, func() {
		if err := dreamReview(); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "share a vendor") {
		t.Errorf("`logos dream review` offered an insight deleted from the queue file:\n%s", out)
	}
	if !strings.Contains(out, "1 insight discarded") {
		t.Errorf("`logos dream review` adopted the deletion without saying so:\n%s", out)
	}
}
