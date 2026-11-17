package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, l := range lines {
		b = append(b, l...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointReceiptsAreReadOnlyFromLogosOwnToolResults(t *testing.T) {
	claude, codex := t.TempDir(), t.TempDir()
	t.Setenv(LogosClaudeProjectsEnv, claude)
	t.Setenv(LogosCodexSessionsEnv, codex)

	writeLines(t, filepath.Join(claude, "-Users-x-kestrel", "sess-a.jsonl"),
		// The receipt itself: the first line of a Logos tool result.
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-09-01T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"✓ Logos · checkpoint saved to logos — sessions/kestrel/20260901-100000-claude-code.md"}]}]}}`,
		// Announcements off: the same receipt without its marker still counts.
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-09-01T11:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"checkpoint saved to logos — sessions/kestrel/wt/_convos/_unattached/20260901-110000-claude-code.md"}]}}`,
		// Before the rename the receipt said brain, sometimes with the path quoted.
		`{"type":"user","sessionId":"sess-b","timestamp":"2026-05-01T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t4","content":[{"type":"text","text":"✓ Logos · checkpoint saved to brain — `+"`"+`sessions/kestrel/20260501-100000-claude-code.md`+"`"+`"}]}]}}`,
		// The CLI's own answer, in the output of the shell command that ran it.
		`{"type":"user","sessionId":"sess-b","timestamp":"2026-05-02T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t5","content":"checkpoint written: sessions/kestrel/20260502-100000-cli.md"}]}}`,
		// A grep whose output quotes another conversation's receipt is not one.
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-09-02T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"log.txt:3: ✓ Logos · checkpoint saved to logos — sessions/kestrel/20260830-090000-claude-code.md"}]}}`,
		// Nor is an agent repeating a receipt to the user in its own words.
		`{"type":"assistant","sessionId":"sess-a","timestamp":"2026-09-02T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"checkpoint saved to logos — sessions/kestrel/20260830-090000-claude-code.md"}]}}`,
	)
	writeLines(t, filepath.Join(codex, "2026", "09", "01", "rollout-2026-09-01T10-00-00-cdx.jsonl"),
		`{"timestamp":"2026-09-01T12:00:00.500Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"cdx-1","item":{"type":"McpToolCall","server":"logos","tool":"checkpoint","result":{"content":[{"type":"text","text":"✓ Logos · checkpoint saved to logos — sessions/acme/20260901-120000-codex.md"}]}}}}`,
		// A shell command printing the same text is not Logos speaking.
		`{"timestamp":"2026-09-01T12:05:00Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"cdx-1","item":{"type":"CommandExecution","aggregated_output":"checkpoint saved to logos — sessions/acme/20260830-000000-codex.md"}}}`,
	)

	got, unreadable, err := CheckpointReceipts()
	if err != nil || len(unreadable) != 0 {
		t.Fatal(err, unreadable)
	}
	want := map[string]Receipt{
		"kestrel/20260901-100000-claude-code":    {HostSession: "sess-a", At: time.Date(2026, 9, 1, 10, 0, 1, 0, time.UTC)},
		"kestrel/wt/20260901-110000-claude-code": {HostSession: "sess-a", At: time.Date(2026, 9, 1, 11, 0, 1, 0, time.UTC)},
		"kestrel/20260501-100000-claude-code":    {HostSession: "sess-b", At: time.Date(2026, 5, 1, 10, 0, 1, 0, time.UTC)},
		"kestrel/20260502-100000-cli":            {HostSession: "sess-b", At: time.Date(2026, 5, 2, 10, 0, 1, 0, time.UTC)},
		"acme/20260901-120000-codex":             {HostSession: "cdx-1", At: time.Date(2026, 9, 1, 12, 0, 0, 5e8, time.UTC)},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d receipts, got %d: %+v", len(want), len(got), got)
	}
	for key, w := range want {
		rs := got[key]
		if len(rs) != 1 || rs[0].HostSession != w.HostSession || !rs[0].At.Equal(w.At) {
			t.Fatalf("%s: want %+v, got %+v", key, w, rs)
		}
	}
}

// One transcript Logos cannot open must not cost every other checkpoint its
// conversation: it is passed over and named, and the rest are still read.
func TestAnUnreadableTranscriptIsPassedOverAndNamed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	claude := t.TempDir()
	t.Setenv(LogosClaudeProjectsEnv, claude)
	t.Setenv(LogosCodexSessionsEnv, t.TempDir())
	writeLines(t, filepath.Join(claude, "-Users-x-kestrel", "sess-a.jsonl"),
		`{"type":"user","sessionId":"sess-a","timestamp":"2026-09-01T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"checkpoint saved to logos — sessions/kestrel/20260901-100000-claude-code.md"}]}}`)
	locked := filepath.Join(claude, "-Users-x-kestrel", "sess-b.jsonl")
	writeLines(t, locked, `{"type":"user","sessionId":"sess-b","timestamp":"2026-09-01T11:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"checkpoint saved to logos — sessions/kestrel/20260901-110000-claude-code.md"}]}}`)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })

	got, unreadable, err := CheckpointReceipts()
	if err != nil {
		t.Fatalf("one unreadable transcript stopped the scan: %v", err)
	}
	if len(unreadable) != 1 || unreadable[0] != locked {
		t.Fatalf("want the locked transcript named, got %v", unreadable)
	}
	if len(got["kestrel/20260901-100000-claude-code"]) != 1 {
		t.Fatalf("the readable transcript's receipt is lost: %+v", got)
	}
}
