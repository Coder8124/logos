package spend

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/transcript"
)

// hosts points both readers at fresh directories and returns them. A host
// left without files still counts as found, which is what a real machine
// with Claude Code installed but unused this month looks like.
func hosts(t *testing.T) (claude, codex string) {
	t.Helper()
	claude, codex = t.TempDir(), t.TempDir()
	t.Setenv(transcript.LogosClaudeProjectsEnv, claude)
	t.Setenv(transcript.LogosCodexSessionsEnv, codex)
	return claude, codex
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func claudeReqLine(ts time.Time, cwd, session, id, req, model string, in, w5, w1, read, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":%q,"sessionId":%q,"requestId":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		stamp(ts), cwd, session, req, id, model, in, w5+w1, read, out, w5, w1)
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestARequestWrittenAsSeveralLinesIsCountedOnce(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	// One response of three content blocks, then the same request copied
	// into a resumed session's file: still one request.
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 20),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 30))
	write(t, filepath.Join(claude, "-p", "b.jsonl"),
		claudeReqLine(now, "/w/proj", "s2", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 30))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Input != 1000 || r.Total.Output != 30 {
		t.Fatalf("total = %+v, want one request of 1000 in and 30 out", r.Total.Usage)
	}
}

func TestClaudeCodesSyntheticMessagesAreNotRequests(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "<synthetic>", 5000, 0, 0, 0, 5))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Total() != 0 {
		t.Fatalf("a <synthetic> line was counted: %+v", r.Total.Usage)
	}
}

func TestEachTokenKindIsPricedAtItsOwnRate(t *testing.T) {
	// claude-opus-5-5: $4 in, $20 out, $0.20 cache read; writes 1.25× and 2× input.
	usd, ok := Cost("claude-opus-5-5[1m]", Usage{Input: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6, CacheRead: 1e6, Output: 1e6})
	if !ok || !near(usd, 4+5+8+0.20+20) {
		t.Fatalf("Cost = %v, %v; want 37.20", usd, ok)
	}
}

func TestAHaikuPromptPastTheLongThresholdUsesTheLongRateCard(t *testing.T) {
	short, _ := Cost("claude-haiku-5-5", Usage{Input: 100_000, Output: 1e6})
	long, _ := Cost("claude-haiku-5-5", Usage{Input: 100_001, Output: 1e6})
	if !near(short, 0.01+0.50) {
		t.Fatalf("at the threshold = %v, want the standard card (0.51)", short)
	}
	if !near(long, 100_001*0.50/1e6+2.50) {
		t.Fatalf("past the threshold = %v, want the long card", long)
	}
}

func TestAModelWithNoListPriceKeepsItsTokensButNoDollars(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "m1", "r1", "flint", 400, 0, 0, 0, 100),
		claudeReqLine(now, "/w/proj", "s1", "m2", "r2", "claude-sonnet-5", 1e6, 0, 0, 0, 0))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Models["flint"]; f == nil || f.USD != 0 || f.Unpriced != 500 {
		t.Fatalf("flint = %+v, want 500 unpriced tokens and no dollars", f)
	}
	if !near(r.Total.USD, 2) || r.Total.Unpriced != 500 {
		t.Fatalf("total = $%v with %d unpriced, want $2 and 500", r.Total.USD, r.Total.Unpriced)
	}
}

func TestASubagentWorktreeCountsAsTheProjectItWorksOn(t *testing.T) {
	for cwd, want := range map[string]string{
		"/u/brain/.claude/worktrees/agent-a2ed589c": "brain",
		"/u/brain":           "brain",
		"/u/worktrees/brain": "brain",
		"":                   "(no project)",
	} {
		if got := projectOf(cwd); got != want {
			t.Errorf("projectOf(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestARequestBeforeTheWindowIsLeftOut(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now.Add(-48*time.Hour), "/w/proj", "s1", "m1", "r1", "claude-sonnet-5", 999, 0, 0, 0, 0),
		claudeReqLine(now, "/w/proj", "s1", "m2", "r2", "claude-sonnet-5", 1, 0, 0, 0, 0))
	r, err := Read(now.Add(-24*time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Input != 1 {
		t.Fatalf("input = %d, want only the request inside the window", r.Total.Input)
	}
}

func TestAProjectFilterKeepsOnlyThatProject(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/alpha", "s1", "m1", "r1", "claude-sonnet-5", 7, 0, 0, 0, 0),
		claudeReqLine(now, "/w/beta", "s2", "m2", "r2", "claude-sonnet-5", 9, 0, 0, 0, 0))
	r, err := Read(now.Add(-time.Hour), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Input != 7 || len(r.Projects) != 1 {
		t.Fatalf("total %d over %v, want alpha's 7 alone", r.Total.Input, r.Projects)
	}
}

func codexCount(ts time.Time, in, cached, out, lastIn int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d},"last_token_usage":{"input_tokens":%d}}}}`,
		stamp(ts), in, cached, out, lastIn)
}

func TestCodexCountsGrowthNotEveryRepeatedTokenCount(t *testing.T) {
	_, codex := hosts(t)
	now := time.Now()
	write(t, filepath.Join(codex, "2026", "rollout.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"cwd":"/w/proj"}}`, stamp(now)),
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"cwd":"/w/proj","model":"gpt-6-luna"}}`, stamp(now)),
		codexCount(now, 1000, 600, 50, 1000),
		codexCount(now, 1000, 600, 50, 1000), // a rate-limit refresh: nothing spent
		codexCount(now, 3000, 2400, 80, 2000))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	// 3000 in of which 2400 cached: 600 uncached, 2400 read, 80 out.
	got := r.Total.Usage
	if got.Input != 600 || got.CacheRead != 2400 || got.Output != 80 {
		t.Fatalf("total = %+v, want 600 uncached, 2400 cached, 80 out", got)
	}
	if len(r.Sessions) != 1 || r.Sessions[0].Peak != 2000 || r.Sessions[0].Model != "gpt-6-luna" {
		t.Fatalf("sessions = %+v, want one gpt-6-luna session peaking at 2000", r.Sessions)
	}
}

func TestAMissingHostDirectoryIsSkippedNotAnError(t *testing.T) {
	t.Setenv(transcript.LogosClaudeProjectsEnv, filepath.Join(t.TempDir(), "absent"))
	t.Setenv(transcript.LogosCodexSessionsEnv, filepath.Join(t.TempDir(), "absent"))
	r, err := Read(time.Now().Add(-time.Hour), "")
	if err != nil || len(r.Hosts) != 0 {
		t.Fatalf("Read = %+v, %v; want no hosts and no error", r, err)
	}
}

func TestATranscriptThatCannotBeOpenedIsCountedNotFatal(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10))
	// A host file gone between the walk and the read: a dangling link is the
	// same thing without the race.
	if err := os.Symlink(filepath.Join(claude, "gone"), filepath.Join(claude, "-p", "b.jsonl")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatalf("one unreadable file failed the whole report: %v", err)
	}
	if r.Skipped != 1 || r.Total.Input != 1000 {
		t.Fatalf("skipped = %d, input = %d; want 1 file skipped and the other's 1000 counted", r.Skipped, r.Total.Input)
	}
}

func TestALineWithABadTimestampIsCountedUnreadable(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	line := strings.Replace(claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10), stamp(now), "yesterday-ish", 1)
	write(t, filepath.Join(claude, "-p", "a.jsonl"), line)
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Unreadable != 1 {
		t.Fatalf("unreadable = %d, want the bad-timestamp line counted", r.Unreadable)
	}
}

func TestCodexTokensBeforeAnyModelIsNamedAreKeptUnpriced(t *testing.T) {
	_, codex := hosts(t)
	now := time.Now()
	write(t, filepath.Join(codex, "2026", "rollout.jsonl"),
		codexCount(now, 1000, 0, 50, 1000))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	b := r.Models[unknownModel]
	if b == nil || b.Total() != 1050 || r.Total.Unpriced != 1050 {
		t.Fatalf("models = %v, unpriced = %d; want 1050 tokens under %q", r.Models, r.Total.Unpriced, unknownModel)
	}
}

func TestClaudeLinesWithNoSessionIdAreOneSessionPerFile(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10))
	write(t, filepath.Join(claude, "-p", "b.jsonl"),
		claudeReqLine(now, "/w/proj", "", "msg_2", "req_2", "claude-opus-5-5", 2000, 0, 0, 0, 10))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 2 {
		t.Fatalf("sessions = %d, want one per file", len(r.Sessions))
	}
}

func TestTwoRequestsWithNoMessageIdInOneSecondAreBothCounted(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "", "", "claude-opus-5-5", 1000, 0, 0, 0, 10),
		claudeReqLine(now, "/w/proj", "s1", "", "", "claude-opus-5-5", 2000, 0, 0, 0, 10))
	r, err := Read(now.Add(-time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Input != 3000 {
		t.Fatalf("input = %d, want both requests' 3000", r.Total.Input)
	}
}
