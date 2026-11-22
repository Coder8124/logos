package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// The month report counts checkpoints from the vault, where they are written,
// and restores from the ledger — and says, beside the one figure that is a
// guess, how the guess was made.
func TestUsageMonthCountsCheckpointsHandoffsAndLabelsTheTimeAnEstimate(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)

	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &session.Checkpoint{
		Project: "kestrel", Agent: "claude-code", Task: "quote the waveguide", State: "waiting on a quote",
		Failed: []string{"route: re-quote | observation: no movement under 10k units"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, e := range []usagepkg.Event{
		{Kind: usagepkg.KindPack, Project: "kestrel", Via: "mcp:resume", Sent: 400, Full: 400, Reader: "cursor", From: "claude-code"},
		{Kind: usagepkg.KindPack, Project: "kestrel", Via: "mcp:resume", Sent: 200, Full: 200, Reader: "claude-code", From: "claude-code"},
		{TS: time.Now().Add(-45 * 24 * time.Hour).Unix(), Kind: usagepkg.KindPack, Project: "kestrel", Via: "mcp:resume", Sent: 9000, Full: 9000, Reader: "cursor", From: "claude-code"},
	} {
		if err := usagepkg.Record(vault, e); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		if err := runUsage([]string{"--month", "--each", "--wpm", "40"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"the last 30 days",
		"1 checkpoint written by an agent",
		"2 restores",
		"1 across agents",
		"1 within one agent",
		"time saved, an ESTIMATE: ≈ 8m across agents, ≈ 4m within one agent",
		"typed out by hand at 40 words a minute",
		"Logos cannot see how long a hand-written summary would have taken",
		"claude-code    → cursor",
		"every checkpoint, oldest first (1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "150m") || strings.Contains(out, "2h 30m") {
		t.Errorf("the 45-day-old restore leaked into a 30-day report:\n%s", out)
	}
	if got := strings.Count(out, "\n20"); got != reportDays {
		t.Errorf("want one dated row for each of the %d days, got %d in:\n%s", reportDays, got, out)
	}
}

func TestUsageMonthOnAnEmptyVaultSaysSo(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	out := captureStdout(t, func() {
		if err := runUsage([]string{"--month"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no checkpoints or restores for every project in the last 30 days") {
		t.Errorf("empty month printed:\n%s", out)
	}
}

func TestUsageRefusesAnUnusableTypingSpeed(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	for _, bad := range []string{"0", "-3", "fast", ""} {
		if err := runUsage([]string{"--month", "--wpm", bad}); err == nil || !strings.Contains(err.Error(), "--wpm needs a typing speed") {
			t.Errorf("--wpm %q: got %v, want a plain refusal", bad, err)
		}
	}
}

func TestMinutesAreWrittenTheWayAPersonSaysThem(t *testing.T) {
	for in, want := range map[float64]string{0: "0m", 0.4: "<1m", 8: "8m", 38.2: "38m", 60: "1h 00m", 135: "2h 15m"} {
		if got := fmtMinutes(in); got != want {
			t.Errorf("fmtMinutes(%v) = %q, want %q", in, got, want)
		}
	}
}
