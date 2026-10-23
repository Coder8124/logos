package main

import (
	"strings"
	"testing"
	"time"

	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// Every number logos usage prints says how it was measured, beside the number:
// a token saving read as the user's API bill is a true number that misleads.
func TestUsageStatesEachNumberWithHowItWasMeasured(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	for _, e := range []usagepkg.Event{
		{Kind: usagepkg.KindPack, Project: "kestrel", Via: "cli:resume", Sent: 2000, Full: 14000, Reader: "codex", From: "claude-code"},
		{Kind: usagepkg.KindPack, Project: "heron", Via: "mcp:context", Sent: 1000, Full: 1000},
		{Kind: usagepkg.KindDeadEnd, Project: "kestrel", Via: "cli:tried", Rulings: 2},
	} {
		if err := usagepkg.Record(vault, e); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		if err := runUsage([]string{"kestrel", "--usd", "3"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"1 context pack sent ~2,000 tokens",
		"what it drew from came to ~14,000 — ~12,000 left out",
		"not your API usage",
		"≈ $0.04 at $3 per million tokens",
		"1 check handed back 2 recorded dead ends",
		"1 restore, 1 of them a handoff",
		"never counted as one",
		"memories accepted: not counted — there is no index yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// An empty ledger is a fresh install, not a broken command: it says where the
// numbers will come from.
func TestUsageOnAnEmptyVaultSaysWhatWillFillIt(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	out := captureStdout(t, func() {
		if err := runUsage(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "nothing recorded for every project yet") {
		t.Errorf("empty ledger printed:\n%s", out)
	}
}

// The week is the last seven days only, and says so in its heading.
func TestUsageWeekLeavesOutWhatCameBeforeIt(t *testing.T) {
	vault := t.TempDir()
	t.Setenv("LOGOS_VAULT", vault)
	old := time.Now().Add(-10 * 24 * time.Hour).Unix()
	for _, e := range []usagepkg.Event{
		{TS: old, Kind: usagepkg.KindPack, Project: "kestrel", Via: "cli:resume", Sent: 9000, Full: 9000},
		{Kind: usagepkg.KindDeadEnd, Project: "kestrel", Via: "cli:tried", Rulings: 1},
		{Kind: usagepkg.KindAvoided, Project: "kestrel", Rulings: 1},
	} {
		if err := usagepkg.Record(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() {
		if err := runUsage([]string{"--week"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"the last 7 days", "0 context packs", "1 check handed back 1 recorded dead end", "1 ruling may have been avoided", "0 restores"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "9,000") {
		t.Errorf("a pack from ten days ago was counted in the week:\n%s", out)
	}
}

func TestUsageRefusesARateThatIsNotAPrice(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	if err := runUsage([]string{"--usd", "cheap"}); err == nil || !strings.Contains(err.Error(), "per million tokens") {
		t.Errorf("--usd cheap = %v, want a refusal naming the unit", err)
	}
}

// The switch announces itself both ways, and a report read while it is off
// says so — otherwise totals that stopped growing read as Logos doing nothing.
func TestUsageOffIsSaidWhenSetAndWhenTheTotalsAreRead(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	out := captureStdout(t, func() {
		if err := runUsage([]string{"off"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "usage recording is off") {
		t.Errorf("turning it off printed:\n%s", out)
	}
	out = captureStdout(t, func() {
		if err := runUsage(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "recording is off") || !strings.Contains(out, "logos stats --usage on") {
		t.Errorf("a report read with the ledger off did not say so:\n%s", out)
	}
	out = captureStdout(t, func() {
		if err := runUsage([]string{"on"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "usage recording is on") {
		t.Errorf("turning it on printed:\n%s", out)
	}
}
