package ingest_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/ingest"
	"github.com/Coder8124/brain/internal/session"
	"github.com/Coder8124/brain/internal/transcript"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func sampleSession() *transcript.Session {
	return &transcript.Session{
		Harness: "codex",
		ID:      "01a05124-f309-7cd3-86e6-d0d303880456",
		Path:    "/home/bob/.codex/sessions/2026/01/05/rollout.jsonl",
		Project: "gadgets",
		Started: 1_736_087_400,
		Ended:   1_736_088_000,
		Hash:    "abc123def456abc123def456abc123def456abc123def456abc123def456aaaa",
		Turns: []transcript.Turn{
			{Role: "user", Text: "deploy it"},
			{Role: "assistant", Text: "running deploy"},
			{Role: "tool", Tool: "exec", Input: "./deploy.sh", Status: "error", Text: "missing AWS_REGION"},
			{Role: "assistant", Text: "setting region"},
			{Role: "tool", Tool: "shell", Input: "AWS_REGION=us-east-1 ./deploy.sh", Status: "ok", Text: "deploy complete"},
			{Role: "tool", Tool: "edit", Input: "internal/deploy/config.go", Status: "ok"},
		},
	}
}

func TestHarvestLeavesVerifiedEmptyWhenThereIsNoModel(t *testing.T) {
	c := ingest.Harvest(sampleSession())

	if len(c.Verified) != 0 || len(c.Failed) != 0 {
		t.Fatalf("harvest must not invent verified/failed: %+v / %+v", c.Verified, c.Failed)
	}
	if c.Tier != ingest.TierHarvest {
		t.Fatalf("tier = %q, want harvest", c.Tier)
	}
	if len(c.Commands) != 2 {
		t.Fatalf("commands = %v, want the two shell invocations", c.Commands)
	}
	joined := strings.Join(c.Commands, "\n")
	if !strings.Contains(joined, "./deploy.sh — failed") || !strings.Contains(joined, "AWS_REGION=us-east-1 ./deploy.sh — ok") {
		t.Fatalf("commands must carry exit status: %v", c.Commands)
	}
	if !contains(c.Files, "internal/deploy/config.go") {
		t.Fatalf("files = %v, want the edited path", c.Files)
	}
}

func TestMarkdownAndParseAreInverses(t *testing.T) {
	c := ingest.Harvest(sampleSession())
	c.Next = "wire the region into CI"
	c.Blockers = []string{"staging cluster is down"}

	got := ingest.Parse(c.Markdown(), c.Filename())

	for _, tc := range []struct {
		name string
		a, b any
	}{
		{"harness", c.Harness, got.Harness},
		{"session", c.SessionID, got.SessionID},
		{"source", c.Source, got.Source},
		{"hash", c.Hash, got.Hash},
		{"project", c.Project, got.Project},
		{"tier", c.Tier, got.Tier},
		{"status", ingest.StatusPending, got.Status},
		{"turns", c.TurnCount, got.TurnCount},
		{"commands", c.Commands, got.Commands},
		{"files", c.Files, got.Files},
		{"blockers", c.Blockers, got.Blockers},
		{"next", c.Next, got.Next},
	} {
		if !reflect.DeepEqual(tc.a, tc.b) {
			t.Errorf("%s: round trip changed %v -> %v", tc.name, tc.a, tc.b)
		}
	}
}

// A candidate note whose frontmatter will not unmarshal must not vanish from
// the queue: the queue is walked off the markdown (invariant 1), so a dropped
// candidate is a silently lost session. The session id is recovered from the
// filename and the note is flagged unreadable, not discarded.
func TestACandidateWithCorruptFrontmatterKeepsItsSessionID(t *testing.T) {
	// "baz" with no colon after a mapping is a YAML syntax error.
	raw := "---\nharness: claude-code\nbaz\n---\n\n## State\n\nsomething happened\n"
	c := ingest.Parse(raw, "claude-code-01a05124-f309-7cd3.md")
	if c.SessionID == "" {
		t.Fatal("corrupt frontmatter dropped the candidate's session id")
	}
	if c.SessionID != "claude-code-01a05124-f309-7cd3" {
		t.Errorf("SessionID = %q, want the filename stem", c.SessionID)
	}
	if !c.FrontmatterUnreadable {
		t.Error("FrontmatterUnreadable was not set for an unparseable note")
	}
}

func TestReingestingTheSameSessionDoesNotDuplicateACandidate(t *testing.T) {
	v := t.TempDir()
	c := ingest.Harvest(sampleSession())

	r1, err := ingest.Put(v, c)
	if err != nil || !r1.Written {
		t.Fatalf("first put: %+v %v", r1, err)
	}
	r2, err := ingest.Put(v, c)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Written {
		t.Fatalf("second put wrote a duplicate: %+v", r2)
	}

	pending, _ := ingest.Pending(v)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
}

func TestAnEditedTranscriptProducesANewCandidateRatherThanSilentlyUpdating(t *testing.T) {
	v := t.TempDir()
	c := ingest.Harvest(sampleSession())
	if _, err := ingest.Put(v, c); err != nil {
		t.Fatal(err)
	}

	edited := c
	edited.Hash = "ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000"
	edited.Commands = append(edited.Commands, "git commit — ok")

	r, err := ingest.Put(v, edited)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Written {
		t.Fatalf("edited transcript should produce a new candidate, got %+v", r)
	}

	pending, _ := ingest.Pending(v)
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2 (original kept, edited added)", len(pending))
	}
}

func TestIngestCandidatesSurviveDeletingTheIndex(t *testing.T) {
	// The queue is the markdown on disk. There is no index in this test at all;
	// Pending must still find the candidate.
	v := t.TempDir()
	if _, err := ingest.Put(v, ingest.Harvest(sampleSession())); err != nil {
		t.Fatal(err)
	}
	pending, _ := ingest.Pending(v)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 from markdown alone", len(pending))
	}
}

func TestAPromotedCandidateRecordsTheHarnessAndSessionItCameFrom(t *testing.T) {
	v := t.TempDir()
	db := testDB(t)
	c := ingest.Harvest(sampleSession())
	if _, err := ingest.Put(v, c); err != nil {
		t.Fatal(err)
	}

	found, abs, ok, err := ingest.Find(v, c.SessionID)
	if err != nil || !ok {
		t.Fatalf("find: %v ok=%v", err, ok)
	}

	cp, err := ingest.Promote(db, v, found, abs)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(v, filepath.FromSlash(cp.Slug)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, c.SessionID) || !strings.Contains(body, "codex") {
		t.Fatalf("promoted checkpoint lost its provenance:\n%s", body)
	}
	if !strings.Contains(body, "(imported)") {
		t.Fatalf("promoted checkpoint should mark git state imported:\n%s", body)
	}

	// And it is no longer offered.
	pending, _ := ingest.Pending(v)
	if len(pending) != 0 {
		t.Fatalf("promoted candidate still pending: %d", len(pending))
	}
}

// Promote guards StatusPromoted but must also refuse StatusRejected: a rejected
// note holds no fresh distillate, so promoting it would mint a checkpoint from
// stale data instead of failing loudly (invariant 4).
func TestARejectedCandidateCannotBePromoted(t *testing.T) {
	v := t.TempDir()
	db := testDB(t)
	c := ingest.Harvest(sampleSession())
	if _, err := ingest.Put(v, c); err != nil {
		t.Fatal(err)
	}
	found, abs, ok, err := ingest.Find(v, c.SessionID)
	if err != nil || !ok {
		t.Fatalf("find: %v ok=%v", err, ok)
	}
	if err := ingest.Reject(abs, found); err != nil {
		t.Fatal(err)
	}
	rejected, abs, ok, err := ingest.Find(v, c.SessionID)
	if err != nil || !ok {
		t.Fatalf("re-find: %v ok=%v", err, ok)
	}
	cp, err := ingest.Promote(db, v, rejected, abs)
	if err == nil {
		t.Fatal("Promote accepted a rejected candidate")
	}
	if cp != nil {
		t.Errorf("Promote returned a checkpoint for a rejected candidate: %+v", cp)
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error does not explain the rejection: %v", err)
	}
}

func TestARejectedCandidateIsNotOfferedAgain(t *testing.T) {
	v := t.TempDir()
	c := ingest.Harvest(sampleSession())
	if _, err := ingest.Put(v, c); err != nil {
		t.Fatal(err)
	}
	found, abs, ok, err := ingest.Find(v, c.SessionID)
	if err != nil || !ok {
		t.Fatalf("find: %v ok=%v", err, ok)
	}
	if err := ingest.Reject(abs, found); err != nil {
		t.Fatal(err)
	}
	pending, _ := ingest.Pending(v)
	if len(pending) != 0 {
		t.Fatalf("rejected candidate still pending: %d", len(pending))
	}
}

// sections() used to return a Go map, which ranges in a random order: a note
// with two "## Verified" headings kept whichever block the map yielded last, so
// the same file parsed to different candidates run to run. The blocks must
// merge in document order, identically every time.
func TestTwoVerifiedBlocksMergeDeterministically(t *testing.T) {
	raw := "---\ntype: ingest_candidate\nharness: codex\nsession: s1\n---\n\n" +
		"## Verified\n\n- go build passes\n\n" +
		"## Files\n\n- main.go\n\n" +
		"## Verified\n\n- go test passes\n"

	first := ingest.Parse(raw, "codex-s1.md").Verified
	if len(first) != 2 || first[0] != "go build passes" || first[1] != "go test passes" {
		t.Fatalf("two Verified blocks did not merge in order: %v", first)
	}
	for i := 0; i < 50; i++ {
		if got := ingest.Parse(raw, "codex-s1.md").Verified; !reflect.DeepEqual(got, first) {
			t.Fatalf("parse %d gave a different result: %v vs %v", i, got, first)
		}
	}
}

// Find prefix-matches a session ref. Two candidates can share a prefix, and it
// used to silently pick the newest — promoting the wrong session with no
// signal. An ambiguous ref must be refused with both full ids named.
func TestAnAmbiguousSessionRefIsRejected(t *testing.T) {
	v := t.TempDir()
	a := ingest.Harvest(sampleSession())
	a.SessionID = "01a05124-aaaa"
	b := ingest.Harvest(sampleSession())
	b.SessionID = "01a05124-bbbb"
	for _, c := range []ingest.Candidate{a, b} {
		if _, err := ingest.Put(v, c); err != nil {
			t.Fatal(err)
		}
	}

	_, _, _, err := ingest.Find(v, "01a05124")
	if err == nil {
		t.Fatal("an ambiguous prefix was silently resolved")
	}
	if !strings.Contains(err.Error(), "01a05124-aaaa") || !strings.Contains(err.Error(), "01a05124-bbbb") {
		t.Errorf("the ambiguity error does not name both candidates: %v", err)
	}

	// A full id is still unambiguous.
	if _, _, ok, err := ingest.Find(v, "01a05124-aaaa"); err != nil || !ok {
		t.Fatalf("an exact id no longer resolves: ok=%v err=%v", ok, err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// Codex names its shell tool exec_command, not exec — the harvest classifier
// knew only the shorter name, so 117 of the 148 tool calls in this machine's
// real Codex transcripts were dropped from Commands and the sessions harvested
// as if nothing had run. A tool name the classifier does not recognise costs a
// whole harness its evidence, silently.
func TestCodexExecCommandCountsAsAShellCommand(t *testing.T) {
	s := &transcript.Session{
		Harness: "codex",
		ID:      "019e3750-2b22-7032-9767-4b1e1869bc02",
		Turns: []transcript.Turn{
			{Role: "tool", Tool: "exec_command", Input: "rg --files", Status: "ok", Text: "internal/ingest/harvest.go"},
			{Role: "tool", Tool: "write_stdin", Input: "y", Status: "ok"},
			{Role: "tool", Tool: "apply_patch", Input: "internal/ingest/harvest.go", Status: "ok"},
		},
	}

	c := ingest.Harvest(s)

	if !contains(c.Commands, "rg --files — ok") {
		t.Fatalf("commands = %v, want the exec_command invocation", c.Commands)
	}
	if !contains(c.Files, "internal/ingest/harvest.go") {
		t.Fatalf("files = %v, want the apply_patch path", c.Files)
	}
}
