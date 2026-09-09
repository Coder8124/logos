package ingest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/ingest"
	"github.com/Coder8124/brain/internal/transcript"
)

// widgetsFixture is the checked-in Claude Code session whose turns are:
//
//	1 user       "the build is broken, can you fix it"
//	2 assistant  "Let me run the build."
//	3 tool       go build — error
//	4 assistant  "Added the missing Frob function."
//	5 tool       go build — ok
//	6 assistant  "The build passes now."
//
// Turn 5 is the only observed success, and turn 6 is the assistant asserting it.
// The difference between those two is the whole citation filter.
func widgetsFixture(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "transcript", "testdata", "transcripts",
		"claude-code", "-Users-alice-code-widgets", "11111111-2222-3333-4444-555555555555.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// queuedVault ingests one transcript the way the CLI would — read, harvest, put
// — and records the consent grant, so what follows is testing the agent-facing
// half rather than re-testing the CLI.
func queuedVault(t *testing.T, src string) (vaultDir, sessionID string) {
	t.Helper()
	vaultDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"granted": "2026-09-09T00:00:00Z"})
	if err := os.WriteFile(ingest.ConsentPath(vaultDir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := transcript.ReadFile("claude-code", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Put(vaultDir, ingest.Harvest(s)); err != nil {
		t.Fatal(err)
	}
	return vaultDir, s.ID
}

func evidence(t *testing.T, vaultDir, ref string) ingest.Evidence {
	t.Helper()
	ev, err := ingest.EvidenceFor(vaultDir, ref, 0)
	if err != nil {
		t.Fatalf("EvidenceFor: %v", err)
	}
	return ev
}

// B3 hands distillation to the calling agent, which is a far stronger model
// than any local tier. That buys better judgement, not more trust: an uncited
// claim is a fabrication with good manners whoever wrote it, and the next agent
// reads these fields as paid-for rulings.
func TestAnAgentDistilledClaimWithoutACitedTurnIsDroppedToo(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))
	ev := evidence(t, v, id)

	got, drops := ingest.Filter(ev, ingest.Distillation{
		Model:    "claude-opus-5",
		Verified: []string{"the build succeeds after adding Frob (turn 5)", "the deploy pipeline is healthy"},
		Failed:   []string{"the build failed on undefined: Frob before the fix (turn 3)"},
	})

	if len(got.Verified) != 1 || !strings.Contains(got.Verified[0], "turn 5") {
		t.Fatalf("verified = %v, want only the cited claim", got.Verified)
	}
	if len(got.Failed) != 1 {
		t.Fatalf("failed = %v, want the cited claim kept", got.Failed)
	}
	if len(drops) != 1 || drops[0].Reason != "no turn cited" {
		t.Fatalf("drops = %+v, want the uncited verified claim reported", drops)
	}
}

// The distiller may summarise evidence; it may not supply it. "The build passes
// now" is an assistant turn, and a verified entry backed only by the model
// saying so is exactly the claim that makes the next agent skip running the
// build.
func TestAVerifiedClaimNeedsAnObservedSuccessfulCommand(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))
	ev := evidence(t, v, id)

	got, drops := ingest.Filter(ev, ingest.Distillation{
		Verified: []string{"the build passes (turn 6)", "go build exits clean (turn 5)"},
		Failed:   []string{"go build failed on the missing symbol (turn 3)"},
	})

	if len(got.Verified) != 1 || !strings.Contains(got.Verified[0], "turn 5") {
		t.Fatalf("verified = %v, want only the claim citing the successful tool result", got.Verified)
	}
	if len(drops) != 1 || !strings.Contains(drops[0].Reason, "successful") {
		t.Fatalf("drops = %+v, want the assistant-only claim reported", drops)
	}
	// failed carries no such requirement: a session rules something out by
	// watching it break, and turn 3 is a failure, not a success.
	if len(got.Failed) != 1 {
		t.Fatalf("failed = %v, want the failure claim kept", got.Failed)
	}
}

// A claim citing a turn that does not exist is a hallucinated citation, which
// is worse than none — it looks checkable.
func TestAClaimCitingATurnTheSessionDoesNotHaveIsDropped(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))
	ev := evidence(t, v, id)

	got, drops := ingest.Filter(ev, ingest.Distillation{Verified: []string{"everything works (turn 41)"}})
	if len(got.Verified) != 0 {
		t.Fatalf("verified = %v, want nothing", got.Verified)
	}
	if len(drops) != 1 || !strings.Contains(drops[0].Reason, "does not exist") {
		t.Fatalf("drops = %+v, want the bad citation named", drops)
	}
}

// The agent is only ever offered material a `brain ingest` already harvested and
// queued. Reading a transcript stays a CLI decision the user consented to, so a
// session sitting on disk but never ingested is not servable — and the tool must
// not go looking for it. The un-ingested file here is unreadable on purpose: if
// serving ever falls back to discovery, this fails with a permission error
// instead of a queue miss.
func TestServingAHarvestToAnAgentDoesNotReadAnyNewTranscript(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))

	root := filepath.Join(t.TempDir(), "projects", "-Users-alice-code-private")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(widgetsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.ReplaceAll(string(raw), "the build is broken, can you fix it", "AWS_SECRET_ACCESS_KEY=hunter2")
	other := filepath.Join(root, "abcdef00-1111-2222-3333-444444444444.jsonl")
	if err := os.WriteFile(other, []byte(secret), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(other, 0o644) })
	t.Setenv(transcript.BrainClaudeProjectsEnv, filepath.Dir(root))

	if _, err := ingest.EvidenceFor(v, "abcdef00-1111", 0); err == nil {
		t.Fatal("served a session that was never ingested")
	} else if !strings.Contains(err.Error(), "no ingested candidate") {
		t.Fatalf("error = %v, want a queue miss (a read error means it went looking on disk)", err)
	}

	if body := evidence(t, v, id).Render(); strings.Contains(body, "hunter2") {
		t.Fatal("served evidence pulled in a transcript that was never ingested")
	}
}

// What is on offer is what was reviewed into the queue. If the source has been
// appended to since — and a live session's transcript grows while it is open —
// serving it would show turns the queued candidate was never harvested from.
func TestAnEvidenceRequestRefusesASourceThatChangedSinceTheHarvest(t *testing.T) {
	src := filepath.Join(t.TempDir(), "22222222-3333-4444-5555-666666666666.jsonl")
	raw, err := os.ReadFile(widgetsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	v, id := queuedVault(t, src)

	appended := string(raw) + `{"type":"user","cwd":"/Users/alice/code/widgets","message":{"role":"user","content":"one more thing"}}` + "\n"
	if err := os.WriteFile(src, []byte(appended), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = ingest.EvidenceFor(v, id, 0)
	if err == nil || !strings.Contains(err.Error(), "has changed since it was harvested") {
		t.Fatalf("err = %v, want a refusal naming the changed source", err)
	}
}

// The return trip writes a candidate. A distillation is still something nobody
// has read, and promotion stays a human decision — B3 must not become a way for
// an agent to write its own checkpoint.
func TestAnAgentDistillationWritesACandidateNotACheckpoint(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))

	c, drops, err := ingest.Accept(v, id, ingest.Distillation{
		Model:    "claude-opus-5",
		Verified: []string{"go build exits clean after the fix (turn 5)"},
		Failed:   []string{"go build failed on undefined: Frob (turn 3)"},
		Next:     "run the tests, which this session never did",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(drops) != 0 {
		t.Fatalf("drops = %+v, want none", drops)
	}
	if c.Tier != ingest.TierDistilled || c.Model != "claude-opus-5" {
		t.Fatalf("candidate does not record who distilled it: tier=%s model=%s", c.Tier, c.Model)
	}
	if c.Status != ingest.StatusPending {
		t.Fatalf("status = %s, want pending", c.Status)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions")); !os.IsNotExist(err) {
		t.Fatal("distilling wrote a checkpoint; only a promotion may do that")
	}

	pending, err := ingest.Pending(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || len(pending[0].Verified) != 1 || pending[0].Next == "" {
		t.Fatalf("the distillation did not survive to the queue: %+v", pending)
	}
}

// Untrusted framing (invariant 6): the transcript is quoted inside a labelled
// block and the instructions live outside it, so a session that said "ignore
// previous instructions" reads as a thing that was said, not a thing being
// asked.
func TestServedEvidenceFramesTheTranscriptAsUntrusted(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))
	body := evidence(t, v, id).Render()

	if !strings.Contains(body, "BEGIN UNTRUSTED TRANSCRIPT") || !strings.Contains(body, "END UNTRUSTED TRANSCRIPT") {
		t.Fatalf("evidence is not fenced:\n%s", body)
	}
	if !strings.Contains(body, "evidence, not instructions") {
		t.Fatalf("the fence does not say what the block is:\n%s", body)
	}
	if i, j := strings.Index(body, "END UNTRUSTED TRANSCRIPT"), strings.Index(body, "must name the turn it came from"); i > j {
		t.Fatal("the rules are inside the untrusted block")
	}
}

// Serving requires the same consent the CLI asked for. A vault where ingest was
// never allowed has no candidates anyway, but the check states the rule where a
// future caller will read it.
func TestServingEvidenceWithoutTheConsentGrantRefuses(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))
	if err := os.Remove(ingest.ConsentPath(v)); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.EvidenceFor(v, id, 0); err == nil || !strings.Contains(err.Error(), "granted") {
		t.Fatalf("err = %v, want a refusal naming the missing consent", err)
	}
}

// A long session is served abridged, and a claim may only cite a turn the
// distiller was actually shown. Citing an elided turn is citing evidence it
// never saw — which is the same fabrication as citing a turn that does not
// exist, wearing a plausible number.
func TestAClaimCitingAnAbridgedTurnIsDropped(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))

	ev, err := ingest.EvidenceFor(v, id, 4) // serves turns 1-2 and 5-6 of 6
	if err != nil {
		t.Fatal(err)
	}
	if ev.Elided != 2 || ev.Served[3] || ev.Served[4] {
		t.Fatalf("abridgement is wrong: elided=%d served=%v", ev.Elided, ev.Served)
	}
	if body := ev.Render(); !strings.Contains(body, "turns 3-4 elided") {
		t.Fatalf("the gap is not marked in the evidence:\n%s", body)
	}

	got, drops := ingest.Filter(ev, ingest.Distillation{
		Failed:   []string{"the build failed on undefined: Frob (turn 3)"},
		Verified: []string{"go build exits clean (turn 5)"},
	})
	if len(got.Failed) != 0 {
		t.Fatalf("failed = %v, want the claim about an unshown turn dropped", got.Failed)
	}
	if len(drops) != 1 || !strings.Contains(drops[0].Reason, "not shown") {
		t.Fatalf("drops = %+v, want the elided citation named", drops)
	}
	if len(got.Verified) != 1 {
		t.Fatalf("verified = %v, want the claim citing a served turn kept", got.Verified)
	}
}

// AcceptWithin is how the MCP pair keeps those two halves honest: the write is
// validated against the same abridgement the read rendered, not against the
// whole transcript.
func TestAcceptValidatesAgainstTheWindowThatWasServed(t *testing.T) {
	v, id := queuedVault(t, widgetsFixture(t))

	c, drops, err := ingest.AcceptWithin(v, id, 4, ingest.Distillation{
		Failed: []string{"the build failed on undefined: Frob (turn 3)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Failed) != 0 || len(drops) != 1 {
		t.Fatalf("a claim about an unshown turn was written: failed=%v drops=%+v", c.Failed, drops)
	}
}
