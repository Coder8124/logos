package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// recordFrom writes the auto record s would leave behind, and makes s the
// transcript that record names.
func recordFrom(t *testing.T, s *transcript.Session) (vault string, c session.Checkpoint) {
	t.Helper()
	vault = t.TempDir()
	rec, _, err := AutoCheckpoint(vault, s, "shop")
	if err != nil || rec == nil {
		t.Fatalf("recorded %v, %v", rec, err)
	}
	was := findTranscript
	findTranscript = func(harness, id string) (*transcript.Session, error) {
		if harness == s.Harness && id == s.ID {
			return s, nil
		}
		return nil, fmt.Errorf("%s has no transcript %s", harness, id)
	}
	t.Cleanup(func() { findTranscript = was })
	return vault, *rec
}

// The record says what ran and nothing it learned; the transcript it names
// still holds the rest. Served by the record's own slug, what an agent reads
// out of it is kept only where it cites a turn, and lands as inferred.
func TestAnAutoRecordTakesWhatAnAgentReadFromItsTranscriptAsInferred(t *testing.T) {
	vault, rec := recordFrom(t, worked())

	ev, err := AutoEvidenceFor(vault, rec.Slug, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Turns) != 3 || ev.Record != rec.Slug {
		t.Fatalf("served %d turns for %q, want the 3 behind %s", len(ev.Turns), ev.Record, rec.Slug)
	}
	if out := ev.Render(); !strings.Contains(out, "ingest_distil") || !strings.Contains(out, rec.Slug) {
		t.Errorf("the evidence does not say how to send the reading back:\n%s", out)
	}

	c, drops, _, err := AcceptAuto(vault, rec.Slug, 0, Distillation{
		Model:    "cursor",
		Verified: []string{"go test ./internal/cart passed (turn 3)", "the crash is fixed (turn 2)"},
		Failed:   []string{"a nil check was not enough", "the first edit to checkout.go missed the empty case (turn 2)"},
		Decided:  []string{"return early on an empty cart, because the user asked for exactly that (turn 1)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := c.Inferred
	if in == nil || len(in.Verified) != 1 || len(in.Failed) != 1 || len(in.Decided) != 1 {
		t.Fatalf("kept %+v, want one of each", in)
	}
	// An edit is not a demonstration, and an uncited claim is not evidence.
	if len(drops) != 2 {
		t.Errorf("dropped %+v, want the verified claim citing an edit and the uncited failure", drops)
	}
	if len(c.Verified)+len(c.Failed)+len(c.Decisions) != 0 {
		t.Errorf("the reading was written as the record's own testimony: %+v", c)
	}
}

// A paraphrase of a transcript carries a pasted key as easily as the
// transcript did, and this write has no agent reviewing what reaches the vault.
func TestAReadingOfATranscriptDoesNotWriteATokenIntoTheRecord(t *testing.T) {
	vault, rec := recordFrom(t, leaky())

	_, _, found, err := AcceptAuto(vault, rec.Slug, 0, Distillation{
		Decided: []string{"upload with GITHUB_TOKEN=" + leakedToken + " (turn 1)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Error("the token was masked without saying so")
	}
	raw, _ := os.ReadFile(filepath.Join(vault, filepath.FromSlash(rec.Slug)+".md"))
	if strings.Contains(string(raw), leakedToken) {
		t.Errorf("the token reached the vault:\n%s", raw)
	}
}

// Only auto records have a transcript behind them to read. A checkpoint an
// agent wrote is its testimony, and a path that climbs out of sessions/ is not
// a record at all.
func TestOnlyAnAutoRecordCanBeServedForReading(t *testing.T) {
	vault, rec := recordFrom(t, worked())
	path := filepath.Join(vault, filepath.FromSlash(rec.Slug)+".md")
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "auto: true\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AutoEvidenceFor(vault, rec.Slug, 0); err == nil {
		t.Error("an agent's own checkpoint was served for distilling")
	}
	if _, err := AutoEvidenceFor(vault, "sessions/../../etc/passwd", 0); err == nil {
		t.Error("a path out of the vault was served")
	}
}
