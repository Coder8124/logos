package spend

import (
	"fmt"
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

func ids(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestARequestWrittenAsSeveralLinesIsOneSample(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	// One response of three content blocks, then the same request copied
	// into a resumed session's file: still one request.
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 20))
	write(t, filepath.Join(claude, "-p", "b.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 30))
	got, err := Samples(now.Add(-time.Hour), ids("s1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got["s1"]) != 1 || got["s1"][0].Context != 1000 {
		t.Fatalf("samples = %v, want the one request once", got["s1"])
	}
}

func TestClaudeCodesSyntheticMessagesAreNotSamples(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "<synthetic>", 5000, 0, 0, 0, 10))
	got, err := Samples(now.Add(-time.Hour), ids("s1"))
	if err != nil || len(got) != 0 {
		t.Fatalf("Samples = %v, %v; want nothing for a placeholder", got, err)
	}
}

func TestAContextCountsEveryInputKindTheModelRead(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "m1", "r1", "claude-opus-5-5", 10, 200, 30, 4000, 99))
	got, _ := Samples(now.Add(-time.Hour), ids("s1"))
	if len(got["s1"]) != 1 || got["s1"][0].Context != 4240 {
		t.Fatalf("samples = %v, want 10 + 230 cache written + 4000 cache read, not the output", got["s1"])
	}
}

func TestAMissingHostDirectoryIsSkippedNotAnError(t *testing.T) {
	t.Setenv(transcript.LogosClaudeProjectsEnv, filepath.Join(t.TempDir(), "absent"))
	t.Setenv(transcript.LogosCodexSessionsEnv, filepath.Join(t.TempDir(), "absent"))
	got, err := Samples(time.Now().Add(-time.Hour), ids("s1"))
	if err != nil || len(got) != 0 {
		t.Fatalf("Samples = %v, %v; want nothing and no error", got, err)
	}
}

func TestATranscriptThatCannotBeOpenedDoesNotHideTheOthers(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10))
	// A host file gone between the walk and the read: a dangling link is the
	// same thing without the race.
	if err := os.Symlink(filepath.Join(claude, "gone"), filepath.Join(claude, "-p", "b.jsonl")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got, err := Samples(now.Add(-time.Hour), ids("s1"))
	if err != nil || len(got["s1"]) != 1 {
		t.Fatalf("Samples = %v, %v; want the readable file's request", got, err)
	}
}

func TestALineWithABadTimestampIsNotASample(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	line := strings.Replace(claudeReqLine(now, "/w/proj", "s1", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10), stamp(now), "yesterday-ish", 1)
	write(t, filepath.Join(claude, "-p", "a.jsonl"), line)
	got, err := Samples(now.Add(-time.Hour), ids("s1"))
	if err != nil || len(got) != 0 {
		t.Fatalf("Samples = %v, %v; want the line dropped, not read as the epoch", got, err)
	}
}

func TestClaudeLinesWithNoSessionIdAreNamedByTheirFile(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "abc.jsonl"),
		claudeReqLine(now, "/w/proj", "", "msg_1", "req_1", "claude-opus-5-5", 1000, 0, 0, 0, 10))
	write(t, filepath.Join(claude, "-p", "def.jsonl"),
		claudeReqLine(now, "/w/proj", "", "msg_2", "req_2", "claude-opus-5-5", 2000, 0, 0, 0, 10))
	got, _ := Samples(now.Add(-time.Hour), ids("abc", "def"))
	if got["abc"][0].Context != 1000 || got["def"][0].Context != 2000 {
		t.Fatalf("samples = %v, want each file its own session", got)
	}
}

func TestTwoRequestsWithNoMessageIdInOneSecondAreBothSampled(t *testing.T) {
	claude, _ := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now, "/w/proj", "s1", "", "", "claude-opus-5-5", 1000, 0, 0, 0, 10),
		claudeReqLine(now, "/w/proj", "s1", "", "", "claude-opus-5-5", 2000, 0, 0, 0, 10))
	got, _ := Samples(now.Add(-time.Hour), ids("s1"))
	if len(got["s1"]) != 2 {
		t.Fatalf("samples = %v, want both requests", got["s1"])
	}
}

func TestSamplesFindASessionThatBeganBeforeAnyWindowByItsHostId(t *testing.T) {
	claude, codex := hosts(t)
	now := time.Now()
	write(t, filepath.Join(claude, "-p", "a.jsonl"),
		claudeReqLine(now.AddDate(0, 0, -40), "/w/proj", "s-old", "m1", "r1", "claude-opus-5-5", 9000, 0, 0, 0, 1),
		claudeReqLine(now.Add(-time.Hour), "/w/proj", "s-old", "m2", "r2", "claude-opus-5-5", 120000, 0, 0, 0, 1),
		claudeReqLine(now, "/w/proj", "s-else", "m3", "r3", "claude-opus-5-5", 999999, 0, 0, 0, 1))
	write(t, filepath.Join(codex, "2026", "rollout-2026-01-01T00-00-00-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"),
		`{"timestamp":"`+stamp(now)+`","type":"session_meta","payload":{"id":"codex-1","cwd":"/w/proj"}}`,
		`{"timestamp":"`+stamp(now)+`","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":70000,"output_tokens":5},"last_token_usage":{"input_tokens":70000}}}}`,
		`{"timestamp":"`+stamp(now)+`","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":70000,"output_tokens":5},"last_token_usage":{"input_tokens":70000}}}}`)
	got, err := Samples(now.Add(-time.Hour*3), ids("s-old", "codex-1", "s-missing"))
	if err != nil {
		t.Fatal(err)
	}
	if PeakAt(got["s-old"], now.Unix()) != 120000 || PeakAt(got["codex-1"], now.Unix()) != 70000 || len(got) != 2 {
		t.Fatalf("Samples = %v, want s-old at 120000, codex-1 at 70000 and nothing for the others", got)
	}
	if len(got["codex-1"]) != 1 {
		t.Fatalf("codex samples = %v, want a repeated token_count counted once", got["codex-1"])
	}
}

func TestAPeakStopsAtTheCutoff(t *testing.T) {
	s := []Sample{{TS: 10, Context: 5}, {TS: 20, Context: 90}, {TS: 30, Context: 400}}
	if got := PeakAt(s, 20); got != 90 {
		t.Fatalf("PeakAt = %d, want 90: the 400 came after the cutoff", got)
	}
	if got := PeakAt(s, 5); got != 0 {
		t.Fatalf("PeakAt before the first sample = %d, want 0", got)
	}
}
