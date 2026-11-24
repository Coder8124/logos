package ingest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
)

const turnsSession = "5e55e55e-0000-4000-8000-00000000abcd"

// claudeJSONL writes n alternating user/assistant lines in Claude Code's format.
func claudeJSONL(t *testing.T, path string, n int, word string) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		fmt.Fprintf(&b, `{"type":%q,"sessionId":%q,"cwd":"/w/kestrel","timestamp":"2026-01-02T10:%02d:00.000Z","message":{"role":%q,"content":"%s line %d"}}`+"\n",
			role, turnsSession, i%60, role, word, i)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitTurns(t *testing.T, ix *index.Index, dir string, turns int) *session.Checkpoint {
	t.Helper()
	c := &session.Checkpoint{Project: "kestrel", Agent: "claude-code", HostSession: turnsSession,
		Task: "spike", Next: "go on", Turns: turns}
	if err := session.Commit(ix.DB, dir, c); err != nil {
		t.Fatal(err)
	}
	return c
}

func openVault(t *testing.T) (string, *index.Index) {
	t.Helper()
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	return dir, ix
}

func TestAPrunedTranscriptIsReportedGoneNotReturnedEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no host file either
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 4)
	second := commitTurns(t, ix, dir, 9)

	_, _, err := ingest.SpanFor(dir, second.Slug)
	if !errors.Is(err, ingest.ErrTranscriptGone) {
		t.Fatalf("want ErrTranscriptGone, got %v", err)
	}
	if !strings.Contains(err.Error(), "the transcript is gone") {
		t.Fatalf("the error does not say so: %v", err)
	}
}

func TestASpanIsReadFromTheArchiveAndNamesIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 4)
	second := commitTurns(t, ix, dir, 9)
	claudeJSONL(t, archivePath(dir), 12, "archived")

	sp, _, err := ingest.SpanFor(dir, second.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if sp.From != 5 || sp.To != 9 || len(sp.Turns) != 5 {
		t.Fatalf("span = %d-%d with %d turns, want 5-9 with 5", sp.From, sp.To, len(sp.Turns))
	}
	out := sp.Render("checkpoint x")
	if !strings.Contains(out, "vault's archive") || !strings.Contains(out, "archived line 5") ||
		strings.Contains(out, "archived line 4") || strings.Contains(out, "archived line 10") {
		t.Fatalf("wrong span or source:\n%s", out)
	}
}

func TestTranscriptSearchIsNeverPartOfADefaultResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 4)
	claudeJSONL(t, archivePath(dir), 6, "zanzibarquokka")

	// Explicit search finds the word, quoted as data.
	hits, searched, _ := ingest.SearchTranscripts(dir, "", "zanzibarquokka line 3", 5)
	if searched != 1 || len(hits) != 1 || hits[0].Turn != 3 {
		t.Fatalf("search = %+v (searched %d)", hits, searched)
	}
	if out := ingest.RenderHits("zanzibarquokka", hits, searched, nil); !strings.Contains(out, "BEGIN UNTRUSTED TRANSCRIPT") {
		t.Fatalf("hits are not fenced:\n%s", out)
	}

	// What a resume carries is the checkpoint history; no transcript word may be in it.
	cps, err := session.History(dir, "kestrel", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cps {
		if strings.Contains(c.Markdown(""), "zanzibarquokka") {
			t.Fatal("archived transcript text reached a checkpoint, and so a resume")
		}
	}
}

func TestRecordedTurnsSurviveDeletingTheIndexAndReindexing(t *testing.T) {
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 4)
	commitTurns(t, ix, dir, 9)
	ix.Close()
	if err := os.Remove(filepath.Join(dir, ".logos", "index.db")); err != nil {
		t.Fatal(err)
	}
	ix2, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()
	if err := session.Init(ix2.DB); err != nil {
		t.Fatal(err)
	}
	cps, err := session.History(dir, "kestrel", 0)
	if err != nil || len(cps) != 2 {
		t.Fatalf("history = %d, %v", len(cps), err)
	}
	last := cps[0]
	for _, c := range cps {
		if c.Turns > last.Turns {
			last = c
		}
	}
	from, to, ok := session.TurnSpan(last, cps)
	if !ok || from != 5 || to != 9 {
		t.Fatalf("span after reindex = %d-%d ok=%v, want 5-9", from, to, ok)
	}
}

func TestACheckpointWhoseTranscriptCannotBeFoundRecordsNoTurnsNotAGuess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &session.Checkpoint{Project: "kestrel", Agent: "claude-code", HostSession: turnsSession}
	reason := ingest.StampTurns(c, "claude-code")
	if c.Turns != 0 || reason == "" {
		t.Fatalf("turns = %d, reason = %q; want zero turns and a reason", c.Turns, reason)
	}
}
