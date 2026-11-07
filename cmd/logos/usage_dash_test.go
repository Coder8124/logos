package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/spend"
	"github.com/Coder8124/logos/internal/transcript"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

func sampleDash() dash {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	d := dash{who: "every project", window: "the last 7 days", now: now, recording: true, rate: 3}
	for i := 6; i >= 0; i-- {
		d.days = append(d.days, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	d.saved = usagepkg.Totals{Packs: 12, Sent: 40_000, Full: 1_240_000, DeadEndChecks: 3, Rulings: 5}
	d.savedDaily = []float64{0, 100, 0, 5000, 200_000, 0, 9000}
	d.spend = &spend.Report{
		Total:    spend.Bucket{Usage: spend.Usage{Input: 5_000_000, Output: 200_000}, USD: 534.25, Unpriced: 27_500_000},
		Models:   map[string]*spend.Bucket{"claude-opus-5-5": {USD: 457.39, Usage: spend.Usage{Input: 4e6}}, "a-model-with-an-extraordinarily-long-name": {Unpriced: 9, Usage: spend.Usage{Input: 9}}},
		Projects: map[string]*spend.Bucket{"brain": {USD: 441.15, Usage: spend.Usage{Input: 3e6}}},
		Days:     map[string]*spend.Bucket{d.days[6]: {USD: 80}},
		Sessions: []spend.Session{{Model: "claude-opus-5-5", Peak: 90_000}, {Model: "claude-opus-5-5", Peak: 400_000}, {Model: "gpt-6-luna", Peak: 30_000}},
		Hosts:    []string{"Claude Code", "Codex"},
	}
	d.used = []memory.Memory{{Text: "the vault is truth\x1b[2J; the index is a cache", Uses: 14}}
	d.dormant = []memory.Memory{{Text: "an old note", Created: now.AddDate(0, 0, -90).Unix()}}
	d.dormantN = 1
	d.recall = &recallFact{text: "route: poll the plans dir | observation: passed on the unfixed code", agent: "claude-code", ruled: now.AddDate(0, 0, -40).Unix(), shown: now.Unix()}
	return d
}

func TestTheUsageDashboardFitsTheTerminalAtAnyWidth(t *testing.T) {
	for _, w := range []int{40, 80, 99, 100, 120, 140, 300} {
		out := renderDash(sampleDash(), w)
		limit := min(max(w, 40), 140)
		for i, line := range strings.Split(out, "\n") {
			// Only the panels are laid out to the width; the header and footer
			// are sentences the terminal wraps.
			boxed := strings.ContainsAny(line[:min(len(line), 3)], "│╭╰")
			if n := utf8.RuneCountInString(line); boxed && n > limit {
				t.Fatalf("width %d: line %d is %d columns: %q", w, i, n, line)
			}
		}
	}
}

func TestTheUsageDashboardDrawsEveryPanel(t *testing.T) {
	for _, w := range []int{80, 120} {
		out := renderDash(sampleDash(), w)
		for _, want := range []string{
			"Tokens saved", "~1.2M tokens left out of 12 packs", "≈ $3.60 at $3 per million",
			"Agent spend", "$534.25", "27.5M tokens from models with no list price", "no list price", "claude-opus-5-5", "brain", spend.PriceSource[:20],
			"Memories", "×14", "Taking space — 1 unused for 60+ days", "90d",
			"Longest recall", "40 days between ruling it out and needing it", "by claude-code",
			"Session size", "gpt-6-luna",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("width %d: missing %q in\n%s", w, want, out)
			}
		}
	}
}

func TestVaultTextOnTheDashboardCannotDriveTheTerminal(t *testing.T) {
	if out := renderDash(sampleDash(), 120); strings.Contains(out, "\x1b") {
		t.Fatal("a memory's escape sequence reached the dashboard")
	}
}

func TestAWideTerminalSetsThePanelsSideBySide(t *testing.T) {
	wide, narrow := renderDash(sampleDash(), 120), renderDash(sampleDash(), 80)
	if !strings.Contains(wide, "╮ ╭") {
		t.Fatal("at 120 columns the panels are not in two columns")
	}
	if strings.Contains(narrow, "╮ ╭") {
		t.Fatal("at 80 columns the panels are side by side")
	}
}

func TestAQuietDayIsADotNotTheLowestBar(t *testing.T) {
	if got := []rune(spark([]float64{0, 1, 8}, 10)); len(got) != 3 || got[0] != '·' || got[1] == '·' || got[2] != '█' {
		t.Fatalf("spark = %q, want a dot, a low bar, a full bar", string(got))
	}
}

func TestAnEmptyDashboardSaysWhatFillsIt(t *testing.T) {
	d := dash{who: "every project", window: "the last 30 days", recording: false, memNote: "no index yet — `logos index` builds it", recallNote: "no index yet — `logos index` builds it"}
	out := renderDash(d, 100)
	for _, want := range []string{"recording is off", "nothing recorded in the last 30 days", "no index yet", "no agent sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestHostNamesOnTheDashboardCannotDriveTheTerminal(t *testing.T) {
	d := sampleDash()
	d.spend.Models["evil\x1b[2Jmodel"] = &spend.Bucket{USD: 999, Usage: spend.Usage{Input: 1}}
	d.spend.Projects["evil\x1b]0;pwn\x07proj"] = &spend.Bucket{USD: 999, Usage: spend.Usage{Input: 1}}
	d.spend.Sessions = append(d.spend.Sessions, spend.Session{Model: "evil\x1b[2Jmodel", Peak: 1}, spend.Session{Model: "evil\x1b[2Jmodel", Peak: 1}, spend.Session{Model: "evil\x1b[2Jmodel", Peak: 1})
	d.recall.agent = "agent\x1b[31m"
	for _, w := range []int{80, 120} {
		if out := renderDash(d, w); strings.Contains(out, "\x1b") {
			t.Fatalf("at %d columns a host-recorded name put an escape on the terminal", w)
		}
	}
}

func TestAnUnreadableLedgerIsAnErrorNotAnEmptyDashboard(t *testing.T) {
	v := t.TempDir()
	t.Setenv(transcript.LogosClaudeProjectsEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	if err := os.MkdirAll(filepath.Join(v, usagepkg.Dir, time.Now().Format("2006-01")+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gatherDash(v, "", 30, 0, time.Now()); err == nil {
		t.Fatal("a ledger that could not be read was drawn as an empty dashboard")
	}
}

func TestARowPricedOnlyInPartSaysSo(t *testing.T) {
	rows := spendRows(map[string]*spend.Bucket{"brain": {USD: 12, Unpriced: 5, Usage: spend.Usage{Input: 10}}}, 60)
	if len(rows) != 1 || !strings.Contains(rows[0], "$12.00+") {
		t.Fatalf("rows = %q, want the partly priced row marked $12.00+", rows)
	}
}

func TestADashboardWithOnlyRestoresIsNotEmpty(t *testing.T) {
	d := sampleDash()
	d.saved = usagepkg.Totals{Restores: 2}
	if lines := strings.Join(d.savedPanel(60), "\n"); strings.Contains(lines, "nothing recorded") {
		t.Fatal("two restores were drawn as nothing recorded")
	}
}
