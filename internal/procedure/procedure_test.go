package procedure

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
)

// As with deadend, the tests that matter are the ones about restraint: a
// checker that fires on everything is worse than no checker.

func corpus() []memory.Memory {
	return []memory.Memory{
		{
			Text: "route: run the chaos tier before calling a durability fix done | " +
				"trap: go test ./... passes with the bug present because the chaos tier is behind a build tag | " +
				"verify: go test -count=1 -tags chaos ./chaos/... | layer: implementation | scope: local | evidence: verified",
			Project: "logos", Agent: "claude", Created: time.Now().Add(-72 * time.Hour).Unix(),
		},
		{
			Text: "route: warm the CDN cache before a launch | " +
				"trap: the first thousand requests time out otherwise | evidence: reported",
			Project: "website", Agent: "cursor", Created: time.Now().Add(-24 * time.Hour).Unix(),
		},
	}
}

// The point of the package: a route worded differently than the original
// still surfaces, with its trap intact.
func TestFindsARouteWordedTheSameWay(t *testing.T) {
	hits, err := Check(corpus(), nil, "", "run the chaos tier before shipping a durability fix", "logos", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the chaos-tier procedure should have been found")
	}
	if !strings.Contains(hits[0].Text, "chaos tier") {
		t.Errorf("wrong procedure led: %q", hits[0].Text)
	}
	if hits[0].Record.Trap == "" {
		t.Error("the trap should have survived parsing")
	}
	if hits[0].Elsewhere {
		t.Error("this was recorded on the project being asked about")
	}
}

// Cross-project transfer must be found and flagged, mirroring deadend.
func TestProceduresFromOtherProjectsAreFlagged(t *testing.T) {
	hits, err := Check(corpus(), nil, "", "warm the CDN cache before launch", "logos", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a procedure from another project should still be found")
	}
	if !hits[0].Elsewhere {
		t.Error("a procedure from another project must be marked as such")
	}
	if out := Render(hits); !strings.Contains(out, "may not transfer") {
		t.Errorf("the render must caveat a cross-project procedure:\n%s", out)
	}
}

// Restraint: an unrelated proposal must come back clean.
func TestUnrelatedApproachesDoNotMatch(t *testing.T) {
	for _, proposal := range []string{
		"add a dark mode to the settings screen",
		"write the release notes for this build",
		"upgrade the CI runner image",
	} {
		hits, err := Check(corpus(), nil, "", proposal, "logos", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) > 0 {
			t.Errorf("%q matched %q — a false interruption is worse than a miss",
				proposal, hits[0].Text)
		}
	}
}

// Unlike deadend, silence about a procedure is not itself a finding — it
// renders nothing, and the caller omits the section.
func TestNoHitsRendersNothing(t *testing.T) {
	if out := Render(nil); out != "" {
		t.Errorf("Render with no hits should be empty, got:\n%s", out)
	}
}

// A trapless record is not a procedure and must be refused at the door.
func TestARecordWithoutATrapIsRefused(t *testing.T) {
	rec := ParseRecord("route: run gofmt before committing")
	if err := Validate(rec); err == nil {
		t.Fatal("a route with no trap should be refused")
	}
}

// A record with both required fields is accepted.
func TestARecordWithRouteAndTrapIsAccepted(t *testing.T) {
	rec := ParseRecord("route: run gofmt before committing | trap: CI fails on unformatted code and the error does not say which file")
	if err := Validate(rec); err != nil {
		t.Errorf("a route with a trap should be accepted: %v", err)
	}
}

// A record missing the route entirely reads as free prose, exactly like a
// deadend entry that predates the schema.
func TestAFreeProseLineIsUnclassified(t *testing.T) {
	rec := ParseRecord("just remember to run the tests")
	if rec.Layer != deadend.LayerUnclassified {
		t.Errorf("a line with no route: key should be unclassified, got %q", rec.Layer)
	}
	if rec.Route != rec.Raw {
		t.Errorf("an unclassified record's Route should equal Raw")
	}
}

// A version-bound procedure old enough that its dependency may have moved is
// marked stale rather than dropped, reusing deadend's staleness rule.
func TestAnOldVersionBoundProcedureIsMarkedPossiblySuperseded(t *testing.T) {
	old := []memory.Memory{{
		Text: "route: call the v1 pagination endpoint | trap: v2 changed the cursor format silently | " +
			"scope: version-bound",
		Project: "logos", Agent: "claude", Created: time.Now().Add(-120 * 24 * time.Hour).Unix(),
	}}
	hits, err := Check(old, nil, "", "call the v1 pagination endpoint again", "logos", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("the procedure should still be found")
	}
	if !hits[0].Stale {
		t.Error("a 120-day-old version-bound procedure should be marked stale")
	}
	if out := Render(hits); !strings.Contains(out, "possibly superseded") {
		t.Errorf("render should caveat a stale procedure rather than drop it:\n%s", out)
	}
}

// The typed fields render, including the trap phrased as a warning rather
// than restated verbatim as an instruction.
func TestRenderShowsTheTrapAndTheVerifyCommand(t *testing.T) {
	hits, err := Check(corpus(), nil, "", "run the chaos tier before shipping a durability fix", "logos", 5)
	if err != nil {
		t.Fatal(err)
	}
	out := Render(hits)
	if !strings.Contains(out, "chaos tier is behind a build tag") {
		t.Errorf("render should surface the trap:\n%s", out)
	}
	if !strings.Contains(out, "go test -count=1 -tags chaos") {
		t.Errorf("render should surface the verify command:\n%s", out)
	}
	if !strings.Contains(out, "implementation") || !strings.Contains(out, "verified") {
		t.Errorf("render should surface the typed tags:\n%s", out)
	}
}

// #219 on the procedure side of the same before_you_try call: "commit" and
// "command" are half of this route's subject, and the embedder, which read both
// whole, put the pair at 0.611. Offered, it sends an agent down a path for an
// approach it never proposed.
func TestAProcedureThatOnlySharesTheApproachsWordsIsNotOffered(t *testing.T) {
	route := "push the release tag with the release commit in one command"
	approach := "anchor verified claims to a commit sha and mark them stale when the files they cover change, re-running the recorded command to re-verify"
	corpus := []memory.Memory{{
		Text:    "route: " + route + " | trap: a separate tag push races the release workflow | evidence: once",
		Project: "logos", Agent: "claude", Created: time.Now().Unix(),
	}}
	p := fixedSimilarity(t, approach, 0.611)

	hits, err := Check(corpus, p, "m", approach, "logos", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("a route sharing two words was offered for %q: %q", approach, hits[0].Text)
	}
}

// fixedSimilarity embeds approach at one point and every other text at cos
// from it.
func fixedSimilarity(t *testing.T, approach string, cos float64) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i, s := range req.Input {
			v := []float32{float32(cos), float32(math.Sqrt(1 - cos*cos))}
			if s == approach {
				v = []float32{1, 0}
			}
			data[i] = map[string]any{"embedding": v}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("Fake", srv.URL, "")
}
