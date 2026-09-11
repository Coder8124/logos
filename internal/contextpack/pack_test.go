package contextpack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/secretary"
	"github.com/Coder8124/brain/internal/session"
)

// fakeEmbedder returns the same vector for every input, so ranking never
// enters into it — only SQL's project filter decides what comes back. Good
// enough to prove scoping without depending on a real model runtime.
func fakeEmbedder(t *testing.T) *provider.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			data[i] = map[string]any{"embedding": []float32{1, 0, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return provider.New("fake", srv.URL, "")
}

// A miniature vault with the shape that matters: a project note that links to a
// constraint note without repeating its words. Retrieval on "cost" will find
// bom-cost; only the edge finds yield-rate. That gap is the reason graph
// expansion is in the pack at all.
func seedVault(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()

	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/kestrel-one.md", `---
type: project
title: Kestrel One
relations:
  - { pred: depends_on, obj: "[[yield-rate]]", conf: 1.0, src: stated }
---
Smart glasses. Target BOM $118, actual $141.20. See [[bom-cost]].
`)
	write("topics/bom-cost.md", `---
type: topic
title: BOM cost
---
The bill of materials sits at $141.20 against a $118 target.
`)
	write("topics/yield-rate.md", `---
type: topic
title: Bonding yield
---
Display bonding runs at 71 percent first-pass. Every scrapped unit
is absorbed by the units that ship.
`)

	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	return ix
}

// The pack must reach a note that the query's own words would never surface,
// because the user's link says it is relevant. This is the case ranked
// retrieval structurally cannot cover.
func TestGraphReachPullsInALinkedNoteRetrievalWouldMiss(t *testing.T) {
	ix := seedVault(t)

	// Embedding is nil, so HybridSearch is skipped entirely: whatever appears
	// here arrived purely through the graph.
	p, err := Build(ix, nil, "", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Project == nil {
		t.Fatal("project note should have resolved")
	}

	var found bool
	for _, h := range p.Notes {
		if strings.Contains(h.Slug, "yield-rate") {
			found = true
			if h.Via == "" {
				t.Error("a graph-reached note must be marked as such, or it reads as a direct match")
			}
		}
	}
	if !found {
		t.Errorf("yield-rate should arrive through depends_on, got %v", slugs(p.Notes))
	}

	out := p.Render()
	if !strings.Contains(out, "reached through the graph") {
		t.Errorf("provenance for graph hits is missing from the render:\n%s", out)
	}
	if !strings.Contains(out, "71 percent") {
		t.Errorf("the pack must carry note *content*, not just titles:\n%s", out)
	}
}

// The checkpoint leads the pack and carries the field that stops repeated work.
func TestCheckpointLeadsTheRenderedPack(t *testing.T) {
	ix := seedVault(t)

	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task:   "cut the BOM",
		Failed: []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:   "quote the single-mic line",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint == nil {
		t.Fatal("checkpoint not picked up")
	}
	out := p.Render()

	if !strings.Contains(out, "Already tried, didn't work") {
		t.Errorf("the failed-approaches section must survive rendering:\n%s", out)
	}
	if i, j := strings.Index(out, "Where we left off"), strings.Index(out, "From the vault"); i < 0 || (j >= 0 && i > j) {
		t.Error("the checkpoint should lead — it is the one section nothing else can reconstruct")
	}
	if !strings.Contains(out, p.Checkpoint.Slug) {
		t.Errorf("the checkpoint should be cited in sources:\n%s", out)
	}
}

// What was demonstrated has to arrive before the prose that merely claims
// things, or the arriving agent reads a confident paragraph first and never gets
// to the part that says which of it was checked.
func TestVerificationLeadsTheCheckpointProse(t *testing.T) {
	ix := seedVault(t)

	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task:     "cut the BOM",
		State:    "The single-mic line is basically done and the tariff numbers look fine.",
		Verified: []string{"the single-mic BOM clears $118 — scripts/bom.py --line single-mic"},
		Blockers: []string{"the tariff table is stale, so no landed cost is trustworthy"},
		Commands: []string{"scripts/bom.py --line single-mic"},
		Failed:   []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:     "refresh the tariff table",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()

	for _, want := range []string{
		"Verified — safe to continue from",
		"clears $118",
		"Known broken — do not build on it",
		"tariff table is stale",
		"Shown by running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from the render:\n%s", want, out)
		}
	}

	verified := strings.Index(out, "Verified — safe to continue from")
	for _, later := range []string{"basically done", "Already tried, didn't work", "Next step"} {
		if i := strings.Index(out, later); i >= 0 && i < verified {
			t.Errorf("%q came before the evidence block:\n%s", later, out)
		}
	}
	// Order within the block: what stands, then what does not, then how to check.
	if i, j := strings.Index(out, "Known broken"), strings.Index(out, "Shown by running"); i > j {
		t.Errorf("blockers should precede the commands that produced the evidence:\n%s", out)
	}
}

// The overwhelming majority of checkpoints in any existing vault carry none of
// this. They must render exactly as they did before — an empty "verified"
// heading claims something was checked and found wanting, which is not the same
// as nobody having recorded it.
func TestACheckpointWithNoVerificationRendersWithoutTheBlock(t *testing.T) {
	ix := seedVault(t)

	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task: "cut the BOM",
		Next: "quote the single-mic line",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()

	if !strings.Contains(out, "Where we left off") || !strings.Contains(out, "quote the single-mic line") {
		t.Errorf("an old-shape checkpoint stopped rendering:\n%s", out)
	}
	for _, unwanted := range []string{"Verified — safe to continue from", "Known broken", "Shown by running"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("empty verification produced a %q heading:\n%s", unwanted, out)
		}
	}
}

// A smaller budget must produce a smaller pack and say what it dropped, rather
// than silently returning less.
func TestSmallerBudgetDropsAndReportsIt(t *testing.T) {
	ix := seedVault(t)

	big, _ := Build(ix, nil, "", Request{Task: "reduce cost", Hint: "kestrel-one", Budget: 4000})
	// Small enough that even after inheriting every other section's unspent
	// share, the notes cannot all fit.
	small, _ := Build(ix, nil, "", Request{Task: "reduce cost", Hint: "kestrel-one", Budget: 40})

	bigOut, smallOut := big.Render(), small.Render()
	if len(smallOut) >= len(bigOut) {
		t.Errorf("a 40-token budget produced %d chars vs %d at 4000", len(smallOut), len(bigOut))
	}
	if small.Budget.Spent > big.Budget.Spent {
		t.Errorf("spend should track the ceiling: %d vs %d", small.Budget.Spent, big.Budget.Spent)
	}
	if len(small.Excluded) == 0 {
		t.Error("dropping content silently is the failure mode this field exists to prevent")
	}
	if !strings.Contains(smallOut, "Left out for space") {
		t.Errorf("exclusions must be visible to the agent:\n%s", smallOut)
	}
}

// Working notes are the uncommitted half of continuity and must be labelled as
// such — an agent should know they were never written down properly.
func TestUncommittedNotesAppearSeparately(t *testing.T) {
	ix := seedVault(t)
	if _, err := session.AddNote(ix.DB, "kestrel-one", "claude", "supplier call moved to Thursday"); err != nil {
		t.Fatal(err)
	}

	p, _ := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	out := p.Render()
	if !strings.Contains(out, "not yet checkpointed") {
		t.Errorf("uncommitted work must be marked uncommitted:\n%s", out)
	}
	if !strings.Contains(out, "supplier call moved") {
		t.Errorf("working note missing:\n%s", out)
	}
}

// Open commitments touching the project sort ahead of unrelated ones.
func TestProjectLoopsSortFirst(t *testing.T) {
	ix := seedVault(t)
	secretary.Add(ix.DB, &secretary.Commitment{Text: "renew the parking permit"})
	secretary.Add(ix.DB, &secretary.Commitment{Text: "send Kestrel One tooling PO"})

	p, _ := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if len(p.OpenLoops) < 2 {
		t.Fatalf("want both loops, got %d", len(p.OpenLoops))
	}
	if !strings.Contains(p.OpenLoops[0].Text, "tooling PO") {
		t.Errorf("the project's own loop should lead, got %q", p.OpenLoops[0].Text)
	}
}

// Asking about something unknown returns a thin pack, never an error. A memory
// layer that errors on an unfamiliar question is one an agent learns to avoid.
func TestUnknownSubjectDegradesInsteadOfFailing(t *testing.T) {
	ix := seedVault(t)

	p, err := Build(ix, nil, "", Request{Task: "something entirely unrelated to this vault"})
	if err != nil {
		t.Fatalf("build should degrade, not fail: %v", err)
	}
	if out := p.Render(); !strings.Contains(out, "No project matched") {
		t.Errorf("want an honest empty result:\n%s", out)
	}
}

// The task alone should find the project when no hint is given — an agent that
// says what it is doing has named the project without knowing it.
func TestProjectResolvesFromTheTaskText(t *testing.T) {
	ix := seedVault(t)

	p, _ := Build(ix, nil, "", Request{Task: "keep working on Kestrel One's cost problem"})
	if p.Project == nil || p.Project.Slug != "projects/kestrel-one" {
		t.Errorf("want the project resolved from the task, got %v", p.Project)
	}
}

func slugs(hits []index.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Slug)
	}
	return out
}

// A working note is a finding someone chose to write down — often the reason a
// whole approach was abandoned. Clipping it to a line length destroys exactly
// the part worth keeping, and does it silently.
func TestWorkingNotesAreNotTruncated(t *testing.T) {
	ix := seedVault(t)
	long := "Vault has NO flash size number for the SoC — only that it was costed at " +
		"'the smaller part' on the $27.80 SoC+memory BOM line. Cannot size an A/B " +
		"partition scheme from the vault; this needs Tomas to supply the part number."
	if _, err := session.AddNote(ix.DB, "kestrel-one", "claude", long); err != nil {
		t.Fatal(err)
	}

	p, _ := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one", Budget: 4000})
	out := p.Render()
	if !strings.Contains(out, "needs Tomas to supply the part number") {
		t.Errorf("the end of the finding was cut off:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("working notes should not be ellipsised at a fixed width:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Time windows.
// ---------------------------------------------------------------------------

// A question that names a period is a question with a filter attached, and the
// filter is the part that decides the answer. Without it the pack returns
// everything it has to "what was I working on last month", which is a good
// answer to a question nobody asked.
func TestATaskThatNamesAPeriodFiltersToIt(t *testing.T) {
	ix := seedVault(t)

	now := time.Now()
	ago := func(days int) int64 { return now.AddDate(0, 0, -days).Unix() }
	for _, n := range []struct {
		days int
		text string
	}{
		{45, "Spent the week on the optics quote comparison."},
		{35, "Ran the drop test series on the magnesium frame."},
		{5, "Started the packaging design review."},
	} {
		if _, err := session.AddNoteAt(ix.DB, "kestrel-one", "user", n.text, ago(n.days)); err != nil {
			t.Fatal(err)
		}
	}

	p, err := Build(ix, nil, "", Request{
		Task: "what was I working on about five weeks ago?",
		Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window == nil {
		t.Fatal("no window was parsed from a task that plainly names one")
	}
	out := p.Render()

	if !strings.Contains(out, "drop test") {
		t.Errorf("the note inside the window did not survive:\n%s", out)
	}
	// The out-of-period notes must be gone from the text, not merely deranked.
	// Listing what was excluded would put it back in the context window, which
	// is the whole thing the filter was for.
	for _, gone := range []string{"packaging design review", "optics quote"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q is outside the window and still in the pack:\n%s", gone, out)
		}
	}
	// A filter nobody can see is a filter nobody can correct.
	if !strings.Contains(out, "about five weeks ago") {
		t.Errorf("the render does not say which window it applied:\n%s", out)
	}
	if p.OutOfWindow != 2 {
		t.Errorf("OutOfWindow = %d, want 2", p.OutOfWindow)
	}
}

// A conflict must be judged on the notes the pack actually renders, not on
// notes the window later drops. Computing it first sees both prices and
// reports a disagreement; the reader who receives the pack after windowing
// only ever sees one of them and has no source for the other half of the
// citation.
func TestConflictsAreJudgedAfterTheWindowNotBefore(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)

	now := time.Now()
	ago := func(days int) string { return now.AddDate(0, 0, -days).Format("2006-01-02") }
	if err := writeNote(ix, "topics/retail-price-early.md", fmt.Sprintf(`---
type: topic
title: Retail price, early quote
first_seen: %s
---
Retail price is set at $199 per the first BOM pass.
`, ago(100))); err != nil {
		t.Fatal(err)
	}
	if err := writeNote(ix, "topics/retail-price-later.md", fmt.Sprintf(`---
type: topic
title: Retail price, revised
first_seen: %s
---
Retail is moving to $229 after the optics quote came back.
`, ago(35))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what was the retail price about five weeks ago?",
		Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window == nil {
		t.Fatal("no window was parsed from a task that plainly names one")
	}

	for _, c := range p.Conflicts {
		if strings.Contains(c, "retail-price-early") {
			t.Errorf("conflict cites a note the window already dropped: %q", c)
		}
	}
	out := p.Render()
	if strings.Contains(out, "$199") {
		t.Errorf("the windowed-out figure still reached the render:\n%s", out)
	}
}

// Since must narrow the pack even when Task carries no time phrase at all —
// the whole reason it exists is to give a caller a window when there is
// nothing to parse.
func TestExplicitSinceNarrowsThePackWithoutATaskPhrase(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)

	now := time.Now()
	ago := func(days int) string { return now.AddDate(0, 0, -days).Format("2006-01-02") }
	if err := writeNote(ix, "topics/old-note.md", fmt.Sprintf(`---
type: topic
title: An old note
first_seen: %s
---
Something recorded three months ago, well outside a week window.
`, ago(90))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	// Without a vector, the note never reaches p.Notes at all — this would pass
	// vacuously (never retrieved) instead of proving the window filtered it.
	if _, err := ix.EmbedPending(embed, "fake-model", 10); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Since: SinceWeek, Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window == nil {
		t.Fatal("an explicit Since must produce a window even with no task phrase")
	}
	for _, h := range p.Notes {
		if h.Slug == "topics/old-note" {
			t.Error("a note 90 days old survived an explicit since:week window")
		}
	}
}

// SinceAll is the explicit override for "no, the whole history" and must
// disable windowing outright, not merely fail to narrow it.
func TestSinceAllDisablesWindowing(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Since: SinceAll, Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window != nil {
		t.Errorf("since:all must produce no window, got %v", p.Window)
	}
}

// An English time phrase in Task is an explicit human statement and must win
// over Since even when the two disagree — Since is a knob set without
// necessarily meaning to override wording the user actually typed.
func TestATaskPhraseOverridesAnExplicitSince(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what happened yesterday", Hint: "kestrel-one", Since: SinceYear, Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window == nil {
		t.Fatal("expected a window from the task phrase")
	}
	if p.Window.Phrase != "yesterday" {
		t.Errorf("task phrase should have won over since:year, got phrase %q", p.Window.Phrase)
	}
}

// commitCheckpointAt writes a checkpoint backdated to ts — Commit only stamps
// TS from the wall clock when the caller leaves it zero, so setting it
// directly is how a test controls the gap inference reasons about without
// waiting for real time to pass.
func commitCheckpointAt(t *testing.T, ix *index.Index, project string, ts int64) {
	t.Helper()
	c := &session.Checkpoint{
		Project: project, Agent: "claude", Task: "map the window inference",
		Next: "write the inference tests", TS: ts,
	}
	if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
		t.Fatal(err)
	}
}

// A checkpoint minutes old reads as a sibling or resumed session — the
// inferred window should stay tight, at a day, and say so.
func TestInferredWindowIsADayForARecentCheckpoint(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	commitCheckpointAt(t, ix, "kestrel-one", now.Add(-40*time.Minute).Unix())

	if err := writeNote(ix, "topics/three-days-old.md", fmt.Sprintf(`---
type: topic
title: Three days old
first_seen: %s
---
Recorded three days ago, outside a one-day inferred window.
`, now.AddDate(0, 0, -3).Format("2006-01-02"))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.EmbedPending(embed, "fake-model", 10); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.WindowInferred {
		t.Fatal("a recent checkpoint should have produced an inferred window")
	}
	if p.Window == nil {
		t.Fatal("expected a window")
	}
	for _, h := range p.Notes {
		if h.Slug == "topics/three-days-old" {
			t.Error("a note 3 days old survived an inferred one-day window")
		}
	}
}

// A checkpoint a few days old reads as the same thread picked back up after a
// night or a weekend — the inferred window widens to a month.
func TestInferredWindowWidensToAMonthAfterAFewDaysGap(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	commitCheckpointAt(t, ix, "kestrel-one", now.AddDate(0, 0, -3).Unix())

	ago := func(days int) string { return now.AddDate(0, 0, -days).Format("2006-01-02") }
	if err := writeNote(ix, "topics/twenty-days-old.md", fmt.Sprintf(`---
type: topic
title: Twenty days old
first_seen: %s
---
Recorded twenty days ago, inside a month-wide inferred window.
`, ago(20))); err != nil {
		t.Fatal(err)
	}
	if err := writeNote(ix, "topics/ninety-days-old.md", fmt.Sprintf(`---
type: topic
title: Ninety days old
first_seen: %s
---
Recorded ninety days ago, outside a month-wide inferred window.
`, ago(90))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.EmbedPending(embed, "fake-model", 10); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.WindowInferred {
		t.Fatal("a several-day-old checkpoint should have produced an inferred window")
	}
	var sawTwenty, sawNinety bool
	for _, h := range p.Notes {
		if h.Slug == "topics/twenty-days-old" {
			sawTwenty = true
		}
		if h.Slug == "topics/ninety-days-old" {
			sawNinety = true
		}
	}
	if !sawTwenty {
		t.Error("a note 20 days old should survive an inferred month window")
	}
	if sawNinety {
		t.Error("a note 90 days old survived an inferred month window")
	}
}

// A checkpoint over a week old is a cold return — history is the point of
// asking, so inference must disable windowing outright, the same as
// since:all.
func TestInferredWindowIsAllForAColdReturn(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	commitCheckpointAt(t, ix, "kestrel-one", now.AddDate(0, 0, -10).Unix())

	if err := writeNote(ix, "topics/ninety-days-old.md", fmt.Sprintf(`---
type: topic
title: Ninety days old
first_seen: %s
---
Recorded ninety days ago.
`, now.AddDate(0, 0, -90).Format("2006-01-02"))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window != nil {
		t.Errorf("a cold return should produce no window, got %v", p.Window)
	}
	if p.WindowInferred {
		t.Error("no window means nothing was inferred either")
	}
}

// No checkpoint at all is indistinguishable from a cold return: there is no
// gap to reason about, so the whole history is the only defensible answer.
func TestInferredWindowIsAllWithNoCheckpoint(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)
	now := time.Now()

	if err := writeNote(ix, "topics/ninety-days-old.md", fmt.Sprintf(`---
type: topic
title: Ninety days old
first_seen: %s
---
Recorded ninety days ago.
`, now.AddDate(0, 0, -90).Format("2006-01-02"))); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "what's the status", Hint: "kestrel-one", Now: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint != nil {
		t.Fatal("this test requires no checkpoint to exist for the scope")
	}
	if p.Window != nil {
		t.Errorf("no checkpoint should produce no window, got %v", p.Window)
	}
}

// A task with no time expression must be left entirely alone. The cost of a
// filter firing on a phrase nobody meant temporally is context removed
// silently, which is worse than no filter at all.
func TestAnOrdinaryTaskIsNotFiltered(t *testing.T) {
	ix := seedVault(t)
	if _, err := session.AddNoteAt(ix.DB, "kestrel-one", "user",
		"Started the packaging design review.", time.Now().AddDate(0, 0, -200).Unix()); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "keep cutting the BOM toward target", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Window != nil {
		t.Errorf("a task with no time expression produced a window: %s", p.Window)
	}
	if !strings.Contains(p.Render(), "packaging design review") {
		t.Error("a 200-day-old note was dropped from an unfiltered request")
	}
}

// If the window empties everything dated, the filter is abandoned rather than
// applied. A period with nothing in it should be reported as empty, not
// answered with a blank page.
func TestAnEmptyWindowIsAbandonedRatherThanApplied(t *testing.T) {
	now := time.Now()
	p := Pack{
		Task: "what did I do about two years ago?",
		Working: []session.Note{
			{Text: "Ran the drop test series.", TS: now.AddDate(0, 0, -3).Unix()},
		},
	}
	p.Budget.Limit = DefaultBudget
	p.applyWindow(Request{Task: p.Task, Now: now.Unix()})

	if p.Window == nil {
		t.Fatal("no window was parsed")
	}
	if !p.WindowEmpty {
		t.Fatal("a window matching nothing should be reported empty")
	}
	out := p.Render()
	if !strings.Contains(out, "drop test") {
		t.Errorf("an empty window suppressed the pack instead of standing down:\n%s", out)
	}
	if !strings.Contains(out, "Nothing recorded falls in that period") {
		t.Errorf("the render does not say the period was empty:\n%s", out)
	}
}

// A fact written relative to the moment of writing is unreadable without the
// date it was written on. Three notes each saying "today", recorded weeks
// apart, are three identical claims until they are dated.
func TestFactsCarryTheDateTheyWereRecorded(t *testing.T) {
	ix := seedVault(t)
	when := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{
		Text: "Signed the Pegatron manufacturing agreement today.",
		Kind: memory.Fact, Source: "manual", Created: when.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "manufacturing"})
	if err != nil {
		t.Fatal(err)
	}
	p.Related, _ = memory.All(ix.DB)
	if out := p.Render(); !strings.Contains(out, "27 Jul 2026") {
		t.Errorf("a recorded fact arrived without its date:\n%s", out)
	}
}

// Related memories must be narrowed to the project being resumed. Recall
// ranks the whole vault by similarity alone, so with vocabulary this similar
// an unscoped call returns the other project's fact ahead of, or alongside,
// this one — exactly what pack.go:245 must not do.
func TestRelatedMemoriesDoNotLeakAcrossProjects(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)

	if _, err := memory.Store(ix.DB, embed, "fake-model", &memory.Memory{
		Text: "Kestrel One's target BOM is $118.", Kind: memory.Fact, Project: "kestrel-one", Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Store(ix.DB, embed, "fake-model", &memory.Memory{
		Text: "Nimbus ships in a matte black casing.", Kind: memory.Fact, Project: "nimbus", Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Related {
		if m.Project != "" && m.Project != "kestrel-one" {
			t.Errorf("related memories leaked project %q into a kestrel-one pack: %q", m.Project, m.Text)
		}
	}
}

// The footer's own spend number must not be the whole story: headings, the
// provenance boundary, Sources and the footer's own text are real bytes on
// the wire that never passed through a spender, and a pack that reports only
// the spent total is under-reporting what it actually costs to read.
func TestFooterReportsTheHonestTotalNotJustSpend(t *testing.T) {
	ix := seedVault(t)

	if err := session.Commit(ix.DB, ix.Vault, &session.Checkpoint{
		Project: "kestrel-one", Agent: "claude",
		Task: "cut the BOM", Next: "quote the single-mic line",
	}); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "continue", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()

	if !strings.Contains(out, "Actual size of this message") {
		t.Fatalf("footer does not report the honest total:\n%s", out)
	}
	if p.Budget.Overhead <= 0 {
		t.Errorf("Budget.Overhead = %d, want > 0 — a pack always renders headings, boundary and sources", p.Budget.Overhead)
	}
	// The total cannot count its own line's tokens without chasing its tail
	// (see renderBudget), so it trails the real render by a line's worth — but
	// no more than that, or the "honest" total is fiction again.
	total := p.Budget.Spent + p.Budget.Overhead
	actual := estimate(out)
	if total > actual {
		t.Errorf("reported total %d exceeds the actual render size %d", total, actual)
	}
	if actual-total > 30 {
		t.Errorf("reported total %d is far short of the actual render size %d", total, actual)
	}
	if total <= p.Budget.Spent {
		t.Errorf("total %d should exceed spend %d — scaffolding and the footer itself are never free", total, p.Budget.Spent)
	}
}

// writeNote drops a file into the seeded vault. Kept beside the tests that use
// it rather than in seedVault, which describes a fixed shape on purpose.
func writeNote(ix *index.Index, rel, body string) error {
	path := filepath.Join(ix.Vault, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// Empty is what lets `brain resume` tell a person "nothing is here, type this"
// instead of handing them the model-facing rendering of a void. The risk it
// carries is drift: a pack that gained content through a field Empty does not
// look at would still claim to be empty, and the CLI would suppress a real
// answer. Build it against a vault that has something, then against one that
// has nothing.
func TestAPackWithAnythingInItIsNotEmpty(t *testing.T) {
	ix := seedVault(t)
	pack, err := Build(ix, nil, "", Request{Task: "keep working on Kestrel One's cost problem"})
	if err != nil {
		t.Fatal(err)
	}
	if pack.Empty() {
		t.Errorf("a pack built from a seeded vault reports empty; resume would print "+
			"\"nothing recorded\" over a real answer:\n%s", pack.Render())
	}
}

func TestAPackFromAVaultWithNothingInItIsEmpty(t *testing.T) {
	dir := t.TempDir()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	pack, err := Build(ix, nil, "", Request{Task: "resume work on myproj", Hint: "myproj"})
	if err != nil {
		t.Fatal(err)
	}
	if !pack.Empty() {
		t.Errorf("a pack from an empty vault does not report empty:\n%s", pack.Render())
	}
}

// A memory is a row, not a file — but the search arm injects memories into the
// same hit stream as vault prose, so every remembered fact that matched the
// query was rendered twice: once under "From the vault" as a bare hit, and
// again under "What you've told me" with its kind, date and confidence. On a
// small pack a quarter of the budget went on saying the same things twice, and
// the footer counted them as two separate categories.
func TestARememberedFactIsNotAlsoRenderedAsAVaultNote(t *testing.T) {
	ix := seedVault(t)
	embed := fakeEmbedder(t)

	fact := "the annual toggle belongs in PricingTable"
	m := memory.Memory{Text: fact, Kind: memory.Fact, Project: "kestrel-one", Source: "manual"}
	if _, err := memory.Store(ix.DB, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.SyncMemories(embed, "fake-model"); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, embed, "fake-model", Request{
		Task: "where does the annual toggle belong?", Hint: "kestrel-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Notes {
		if h.Kind == "memory" {
			t.Errorf("a memory reached the vault-prose section as %q: %q", h.Slug, h.Body)
		}
	}
	if n := strings.Count(p.Render(), fact); n != 1 {
		t.Errorf("the fact is rendered %d times, want 1:\n%s", n, p.Render())
	}
}
