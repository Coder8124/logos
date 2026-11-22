package ingest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// archivePath is where the archive sits beside the checkpoints filed so far,
// in the conversation's own folder, which is where a harvest puts it.
func archivePath(dir string) string {
	cps, err := session.History(dir, "kestrel", 0)
	if err != nil || len(cps) == 0 {
		return filepath.Join(dir, session.CheckpointDir, "kestrel", session.ConvosDir, turnsSession, "transcript.jsonl")
	}
	return filepath.Join(dir, filepath.Dir(filepath.FromSlash(cps[0].Slug)), "transcript.jsonl")
}

func TestARecordedHarnessIsTheOneTheSpanIsReadWithWhateverTheAgentIsCalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	c := &session.Checkpoint{Project: "kestrel", Agent: "reviewer", Harness: "claude-code",
		HostSession: turnsSession, Task: "review", Next: "go on", Turns: 6}
	if err := session.Commit(ix.DB, dir, c); err != nil {
		t.Fatal(err)
	}
	claudeJSONL(t, archivePath(dir), 8, "reviewed")

	cps, err := session.History(dir, "kestrel", 0)
	if err != nil || len(cps) != 1 || cps[0].Harness != "claude-code" {
		t.Fatalf("the harness did not round-trip through the vault: %+v, %v", cps, err)
	}
	sp, _, err := ingest.SpanFor(dir, c.Slug)
	if err != nil {
		t.Fatalf("an agent called reviewer made its transcript unreadable: %v", err)
	}
	if len(sp.Turns) != 6 {
		t.Fatalf("span holds %d turns, want 6", len(sp.Turns))
	}
}

func TestTheMaxTurnsCapServesTheLastTurnsAndSaysHowManyWereLeftOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	c := commitTurns(t, ix, dir, 300)
	claudeJSONL(t, archivePath(dir), 300, "long")

	sp, _, err := ingest.SpanFor(dir, c.Slug)
	if err != nil {
		t.Fatal(err)
	}
	sp = sp.Cap(0)
	if sp.From != 181 || sp.To != 300 || len(sp.Turns) != 120 || sp.Omitted != 180 {
		t.Fatalf("cap = %d-%d, %d turns, %d omitted; want 181-300, 120, 180", sp.From, sp.To, len(sp.Turns), sp.Omitted)
	}
	out := sp.Render("checkpoint x")
	if !strings.Contains(out, "180 turns are left out") || strings.Contains(out, "long line 180\n") {
		t.Fatalf("the cut is not announced or the wrong end was kept:\n%s", out)
	}
}

func TestARawArchivesTokenIsMaskedInAServedSpanAndInSearchSnippets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	c := commitTurns(t, ix, dir, 4)
	tok := "ghp_" + strings.Repeat("a1B2c3", 6)
	claudeJSONL(t, archivePath(dir), 6, "token "+tok)

	sp, _, err := ingest.SpanFor(dir, c.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if out := sp.Render("x"); strings.Contains(out, tok) {
		t.Fatalf("a served span leaked the archive's token:\n%s", out)
	}
	hits, _, _ := ingest.SearchTranscripts(dir, "", "token", 5)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		if strings.Contains(h.Snippet, tok) {
			t.Fatalf("a search snippet leaked the token: %s", h.Snippet)
		}
	}
}

func TestATranscriptShorterThanTheRecordedCountIsRefusedNotServedShort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	c := commitTurns(t, ix, dir, 9)
	claudeJSONL(t, archivePath(dir), 5, "rewritten")

	_, _, err := ingest.SpanFor(dir, c.Slug)
	if err == nil || !strings.Contains(err.Error(), "it was rewritten, and nothing is served") {
		t.Fatalf("a short transcript was not refused: %v", err)
	}
}

func TestCaseFoldingThatChangesByteLengthDoesNotPanicTheSearch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 2)
	// U+0130 lowers to a longer string and U+212A (Kelvin) to a shorter one;
	// either made a byte index found in the lowered copy wrong in the original.
	claudeJSONL(t, archivePath(dir), 2, strings.Repeat("İ", 120)+" needle "+strings.Repeat("K", 120))

	for _, q := range []string{"needle", "NEEDLE", "k"} {
		hits, _, _ := ingest.SearchTranscripts(dir, "", q, 5)
		if len(hits) == 0 {
			t.Fatalf("query %q found nothing", q)
		}
	}
}

func TestASearchStopsAtItsLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 2)
	claudeJSONL(t, archivePath(dir), 40, "needle")

	hits, _, _ := ingest.SearchTranscripts(dir, "", "needle", 3)
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want exactly the limit of 3", len(hits))
	}
}

func TestAnArchiveOfUnknownHarnessIsCountedAsSkippedNotParsedAsClaudeCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, _ := openVault(t)
	claudeJSONL(t, archivePath(dir), 4, "needle") // no checkpoint beside it

	hits, searched, skipped := ingest.SearchTranscripts(dir, "", "needle", 5)
	if len(hits) != 0 || searched != 0 || len(skipped) != 1 || !strings.Contains(skipped[0], "unknown harness") {
		t.Fatalf("hits %d searched %d skipped %v", len(hits), searched, skipped)
	}
	if out := ingest.RenderHits("needle", hits, searched, skipped); !strings.Contains(out, "unknown harness") {
		t.Fatalf("the skip is not said:\n%s", out)
	}
}

func TestATranscriptSearchCanBeRestrictedToOneProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, ix := openVault(t)
	commitTurns(t, ix, dir, 2)
	claudeJSONL(t, archivePath(dir), 4, "needle")

	if hits, _, _ := ingest.SearchTranscripts(dir, "other", "needle", 5); len(hits) != 0 {
		t.Fatalf("a search of another project found %d hits", len(hits))
	}
	if hits, _, _ := ingest.SearchTranscripts(dir, "kestrel", "needle", 5); len(hits) == 0 {
		t.Fatal("a search of the project found nothing")
	}
}

var _ = errors.Is
var _ = os.Stat
var _ = transcript.Harnesses
