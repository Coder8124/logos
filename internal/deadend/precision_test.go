package deadend

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/session"
)

// similarTo is a runtime that places each ruling at a fixed cosine from the
// proposal, so a test states the score nomic-embed-text measured for a pair
// rather than hoping a real model reproduces it. A ruling is found by a phrase
// it contains; anything unlisted sits at right angles to the proposal.
func similarTo(t *testing.T, proposal string, cos map[string]float64) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i, s := range req.Input {
			v := []float32{0, 1}
			if s == proposal {
				v = []float32{1, 0}
			}
			for phrase, c := range cos {
				if strings.Contains(s, phrase) {
					v = []float32{float32(c), float32(math.Sqrt(1 - c*c))}
				}
			}
			data[i] = map[string]any{"embedding": v}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("Fake", srv.URL, "")
}

func ruledOut(t *testing.T, failed ...string) (string, *sql.DB) {
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
	if err := session.Commit(ix.DB, dir, &session.Checkpoint{
		Project: "brain", Agent: "claude-code", Task: "work", Failed: failed, Next: "carry on",
	}); err != nil {
		t.Fatal(err)
	}
	return dir, ix.DB
}

// #219, as reported: a proposal to check diffs against dead ends in a
// pre-commit hook was told it repeated a ruling about a mistyped --no-verify
// flag. They share git's vocabulary and nothing else — three words of a long
// ruling — and git's vocabulary alone carries nomic-embed-text to 0.746, just
// over the floor. Similarity at the floor with nothing shared is a topic, not
// an approach.
func TestAProposalThatOnlySoundsLikeARulingIsNotReportedAsTried(t *testing.T) {
	ruling := "Mid-session, a mistyped --no-verify=false flag caused a commit to silently not happen while two subsequent add/commit calls in the same batch ran anyway, merging three unrelated file groups into one commit"
	proposal := "check a git diff or pull request against recorded dead ends in CI or a pre-commit hook, so a change that reintroduces a ruled-out approach is flagged"
	dir, db := ruledOut(t, ruling)

	hits, err := Check(dir, db, similarTo(t, proposal, map[string]float64{"--no-verify": 0.746}), "m", proposal, "brain", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("a proposal about the same tools was reported as a repeat of %q", hits[0].Text)
	}
}

// #219's other half came through the lexical arm: "commit" and "command" are
// half of a short ruling about pushing a tag, so a proposal to anchor claims to
// a commit sha scored exactly Related. The embedder, which read both whole,
// put them at 0.611 — it had the evidence that they are different approaches
// and was not asked.
func TestAProposalThatOnlySharesARulingsWordsIsNotReportedAsTried(t *testing.T) {
	ruling := "push the v0.4.7 tag with the release commit in one command"
	proposal := "anchor verified claims to a commit sha and mark them stale when the files they cover change, re-running the recorded command to re-verify"
	dir, db := ruledOut(t, ruling)

	hits, err := Check(dir, db, similarTo(t, proposal, map[string]float64{"v0.4.7 tag": 0.611}), "m", proposal, "brain", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("a proposal sharing two words was reported as a repeat of %q", hits[0].Text)
	}
}

// The other side of the same bar, with scores measured under nomic-embed-text:
// the fix must not cost the matches the checker exists for. A restatement with
// a middling embedding, a paraphrase just over the floor that shares a few
// words, and one in wholly different words well above it.
func TestARulingIsStillFoundWhenEitherArmHasTheOtherBehindIt(t *testing.T) {
	for _, c := range []struct {
		ruling, proposal, phrase string
		cos                      float64
	}{
		{"Switching to a plastic frame — fails the drop test at 1.2m", "switch to a plastic frame to save weight", "plastic frame", 0.694},
		{"re-send webhooks that failed, waiting longer between each attempt — the queue backed up for hours", "retry failed webhook deliveries with exponential backoff", "re-send webhooks", 0.745},
		{"parallelising the integration suite — shared fixtures made it flaky", "fan the end-to-end tests out across runners", "integration suite", 0.82},
	} {
		dir, db := ruledOut(t, c.ruling)
		hits, err := Check(dir, db, similarTo(t, c.proposal, map[string]float64{c.phrase: c.cos}), "m", c.proposal, "brain", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Errorf("%q was not found for %q at cosine %.3f", c.ruling, c.proposal, c.cos)
		}
	}
}
