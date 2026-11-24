package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
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
	d.restores = restoreSavings{Restores: 4, Measured: 3, NoTranscript: 1, Saved: 540_000, Sent: 1_500, Baseline: 180_000, OtherPacks: 8, OtherSaved: 700_000}
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
			"Tokens saved", "~540K tokens not reloaded across 3 restores", "each sent ~1.5K in place of a ~180K session context",
			"≈ $1.62 at $3 per million", "1 restore had no transcript on this machine", "+ ~700K left out of 8 context packs",
			"baseline: the restored session's largest context",
			"Memories", "×14", "Taking space — 1 unused for 60+ days", "90d",
			"Longest recall", "40 days between ruling it out and needing it", "by claude-code",
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
	for _, want := range []string{"recording is off", "nothing recorded in the last 30 days", "no index yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestAgentNamesOnTheDashboardCannotDriveTheTerminal(t *testing.T) {
	d := sampleDash()
	d.recall.agent = "agent\x1b[31m"
	for _, w := range []int{80, 120} {
		if out := renderDash(d, w); strings.Contains(out, "\x1b") {
			t.Fatalf("at %d columns a recorded name put an escape on the terminal", w)
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

func TestADashboardWithOnlyRestoresIsNotEmpty(t *testing.T) {
	d := sampleDash()
	d.saved = usagepkg.Totals{Restores: 2}
	if lines := strings.Join(d.savedPanel(60), "\n"); strings.Contains(lines, "nothing recorded") {
		t.Fatal("two restores were drawn as nothing recorded")
	}
}

func TestTheDashboardNoLongerDrawsAgentSpendOrSessionSize(t *testing.T) {
	for _, w := range []int{80, 120} {
		out := renderDash(sampleDash(), w)
		for _, gone := range []string{"Agent spend", "Session size", "Top models"} {
			if strings.Contains(out, gone) {
				t.Errorf("width %d: the dashboard still draws %q", w, gone)
			}
		}
	}
}

// commitAt writes a checkpoint at a chosen time through the real writer, so
// the test finds it the way the dashboard does: by reading the vault.
func commitAt(t *testing.T, vault string, c session.Checkpoint) {
	t.Helper()
	ix, err := index.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vault, &c); err != nil {
		t.Fatal(err)
	}
}

func claudeLine(ts time.Time, session string, prompt int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":"/w/kestrel","sessionId":%q,"requestId":"r%d","message":{"id":"m%d","model":"claude-opus-5-5","usage":{"input_tokens":%d,"output_tokens":10}}}`,
		ts.UTC().Format(time.RFC3339Nano), session, prompt, prompt, prompt)
}

func restoreFixture(t *testing.T, withTranscript bool) (vault string, now time.Time) {
	t.Helper()
	vault, claude := t.TempDir(), t.TempDir()
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv(transcript.LogosClaudeProjectsEnv, claude)
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	now = time.Now()
	// The session began three weeks ago, well before a 7-day window, and
	// reached 200K at its largest.
	ended := now.Add(-2 * time.Hour)
	commitAt(t, vault, session.Checkpoint{Project: "kestrel", Agent: "a", Task: "fix checkout", Next: "replay the queue", HostSession: "sess-big", TS: ended.Unix()})
	if withTranscript {
		body := strings.Join([]string{
			claudeLine(now.AddDate(0, 0, -21), "sess-big", 50_000),
			claudeLine(now.Add(-3*time.Hour), "sess-big", 200_000),
			claudeLine(now.Add(-3*time.Hour), "sess-other", 900_000),
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A restore after the checkpoint, and an earlier one before it that must
	// not be matched to it.
	for _, e := range []usagepkg.Event{
		{TS: now.Add(-time.Hour).Unix(), Kind: usagepkg.KindPack, Project: "kestrel", Via: "mcp:resume", Sent: 1_500, Full: 1_500},
	} {
		if err := usagepkg.Record(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	return vault, now
}

func TestARestoresSavingIsMeasuredAgainstTheRestoredSessionsPeakContext(t *testing.T) {
	vault, now := restoreFixture(t, true)
	d, err := gatherDash(vault, "kestrel", 7, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	r := d.restores
	if r.Measured != 1 || r.NoTranscript != 0 || r.Saved != 200_000-1_500 || r.Baseline != 200_000 {
		t.Fatalf("restores = %+v, want one measured against the 200K peak (not the other session's 900K), saving 198.5K", r)
	}
	if out := renderDash(d, 120); !strings.Contains(out, "~198K tokens not reloaded across 1 restore") {
		t.Errorf("the panel does not state the measured saving:\n%s", out)
	}
}

func TestARestoreWithNoTranscriptCountsAsNoBaselineAndSaysSo(t *testing.T) {
	vault, now := restoreFixture(t, false)
	d, err := gatherDash(vault, "kestrel", 7, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.restores; r.Measured != 0 || r.NoTranscript != 1 || r.Saved != 0 {
		t.Fatalf("restores = %+v, want no saving and one with no baseline", r)
	}
	out := renderDash(d, 120)
	if !strings.Contains(out, "1 restore had no transcript on this machine") {
		t.Errorf("the panel does not say a restore had no baseline:\n%s", out)
	}
}

// The prompt hook's showings were counted as activity, so the panel stopped
// saying "nothing recorded", and then printed no line for them.
func TestTheSavedPanelSaysHowManyRulingsWereShownAtThePrompt(t *testing.T) {
	d := sampleDash()
	d.saved = usagepkg.Totals{AtPrompt: 2}
	if out := strings.Join(d.savedPanel(80), "\n"); !strings.Contains(out, "2 rulings shown at the prompt") {
		t.Errorf("the panel does not say what the prompt hook did:\n%s", out)
	}
}
