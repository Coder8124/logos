package contextpack

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
)

func autoRecordFor(t *testing.T, vault string) session.Checkpoint {
	t.Helper()
	c, err := session.WriteAuto(vault, session.Checkpoint{
		Project: "kestrel-one", Agent: "claude-code", Task: "keep going on the bom",
		State: session.ActivityLogStateFor("lost-1"), Files: []string{"bom.csv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A session that ended without a checkpoint leaves a record of what ran, and
// the transcript behind it still says what worked. Resume has to say that the
// reading is there to be had — and how — or nobody pays for it.
func TestResumeOffersToReadWhatAnAutoRecordsTranscriptConcluded(t *testing.T) {
	ix := seedVault(t)
	c := autoRecordFor(t, ix.Vault)

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !strings.Contains(out, "ingest_harvest") || !strings.Contains(out, c.Slug) {
		t.Errorf("resume did not offer the reading of %s:\n%s", c.Slug, out)
	}
}

// Once read, the offer goes and the reading shows — under headings that say
// on every section whose it is, apart from the record's own "already tried",
// so an inferred dead end cannot pass for a stated one.
func TestResumeShowsWhatWasInferredApartFromWhatAnAgentStated(t *testing.T) {
	ix := seedVault(t)
	c := autoRecordFor(t, ix.Vault)
	if _, err := session.InferAuto(ix.Vault, c.Slug, session.Inference{By: "cursor",
		Failed:  []string{"re-quoting the display stack from the old vendor went nowhere (turn 4)"},
		Decided: []string{"kept the 12mm hinge, because the retailer signed off on it (turn 2)"},
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Now: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if strings.Contains(out, "ingest_harvest") {
		t.Errorf("resume offered a reading that was already done:\n%s", out)
	}
	if !strings.Contains(out, "Didn't work, inferred from the transcript by cursor, unverified — not a ruling") ||
		!strings.Contains(out, "Decided, inferred from the transcript by cursor") {
		t.Errorf("the inference is not shown, or not labelled as inferred:\n%s", out)
	}
	if strings.Contains(out, "Already tried, didn't work") || strings.Contains(out, "Ruled out earlier") {
		t.Errorf("an inferred failure was shown as a ruling:\n%s", out)
	}
}
