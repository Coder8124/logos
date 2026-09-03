// Package enginetest exercises the module's public facade from outside it.
//
// It lives in its own directory, and imports github.com/Coder8124/brain like
// any other consumer, so it can only reach what an embedder can reach. If
// something here needs an internal import, the facade is incomplete and that is
// the bug these files exist to catch.
package enginetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain"
)

// open makes a scratch vault. No model runtime — an embedder's CI has none, and
// everything below must work without one.
func open(t *testing.T) (*brain.Brain, string) {
	t.Helper()
	dir := t.TempDir()
	b, err := brain.Open(dir, brain.WithoutEmbedding(), brain.WithAgent("test-agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b, dir
}

func TestOpenRefusesAVaultThatIsNotThere(t *testing.T) {
	if _, err := brain.Open(filepath.Join(t.TempDir(), "nope"), brain.WithoutEmbedding()); err == nil {
		t.Fatal("opening a missing vault should fail, not silently create one")
	}
}

// The handoff, end to end and through the public API only: one agent works,
// records what failed, and stops; a second agent asks where things stand.
func TestHandoffAcrossAgents(t *testing.T) {
	b, _ := open(t)

	if err := b.Note("kestrel-one", "re-quoted the waveguide; no movement under 10k units"); err != nil {
		t.Fatalf("Note: %v", err)
	}
	notes, err := b.Notes("kestrel-one")
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "waveguide") {
		t.Fatalf("uncommitted note did not come back: %+v", notes)
	}

	slug, err := b.Checkpoint(brain.Checkpoint{
		Project:   "kestrel-one",
		Task:      "cut the BOM to target",
		State:     "BOM is $4.20 over",
		Decisions: []string{"hold the aluminium frame"},
		Failed:    []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:      "quote the display driver alternatives",
	})
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if slug == "" {
		t.Fatal("checkpoint returned no slug")
	}

	hist, err := b.History("kestrel-one", 5)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || hist[0].Agent != "test-agent" {
		t.Fatalf("history did not record the agent: %+v", hist)
	}

	projects, err := b.Projects()
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 || projects[0] != "kestrel-one" {
		t.Fatalf("projects = %v, want [kestrel-one]", projects)
	}

	// The arriving agent's whole job is this call.
	c, err := b.Resume("kestrel-one")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	out := c.Render()
	for _, want := range []string{"waveguide", "display driver"} {
		if !strings.Contains(out, want) {
			t.Errorf("resume dropped %q:\n%s", want, out)
		}
	}
}

func TestResumeNeedsAProject(t *testing.T) {
	b, _ := open(t)
	if _, err := b.Resume("  "); err == nil {
		t.Fatal("expected an error for an empty project")
	}
}

// The intercept: a dead end recorded on one project is found when another
// project proposes it, and flagged as possibly not transferring.
func TestTriedFindsARuledOutApproachAcrossProjects(t *testing.T) {
	b, _ := open(t)

	if _, err := b.Checkpoint(brain.Checkpoint{
		Project: "kestrel-one",
		Failed:  []string{"switching to a plastic frame — fails the drop test at 1.2m"},
	}); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	rulings, err := b.Tried("switch to a plastic frame to save weight", "harrier-two")
	if err != nil {
		t.Fatalf("Tried: %v", err)
	}
	if len(rulings) == 0 {
		t.Fatal("the recorded dead end was not intercepted")
	}
	if !rulings[0].Elsewhere {
		t.Error("a ruling from another project should be marked Elsewhere")
	}

	prose := brain.Explain("switch to a plastic frame to save weight", rulings)
	if !strings.Contains(prose, "drop test") || !strings.Contains(prose, "evidence, not a verdict") {
		t.Errorf("Explain lost the finding or the caveat:\n%s", prose)
	}

	// Silence must not read as approval.
	none := brain.Explain("machine the frame from billet", nil)
	if !strings.Contains(none, "No record") {
		t.Errorf("empty rulings should say so explicitly:\n%s", none)
	}
}

func TestMemoryRoundTrip(t *testing.T) {
	b, _ := open(t)

	r, err := b.Remember("I prefer terse replies with no preamble", brain.Preference)
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if r.ID == 0 {
		t.Fatalf("receipt carried no id: %+v", r)
	}

	all, err := b.Memories()
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 memory, got %d", len(all))
	}

	if _, err := b.Recall("how should replies be written", 5); err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if err := b.Forget(r.ID); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if all, _ = b.Memories(); len(all) != 0 {
		t.Fatalf("Forget left %d memories behind", len(all))
	}
}

// The durability claim, from outside: delete the cache, reopen, reindex, and
// everything is still there — because the markdown is the record.
func TestTheCacheIsReallyACache(t *testing.T) {
	b, dir := open(t)

	if _, err := b.Remember("the kestrel BOM target is $38", brain.Fact); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := b.Checkpoint(brain.Checkpoint{Project: "kestrel-one", Next: "quote the driver"}); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if _, err := b.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	b.Close()

	if err := os.RemoveAll(filepath.Join(dir, ".brain")); err != nil {
		t.Fatalf("removing the cache: %v", err)
	}

	b2, err := brain.Open(dir, brain.WithoutEmbedding())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer b2.Close()
	if _, err := b2.Index(); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	mems, err := b2.Memories()
	if err != nil {
		t.Fatalf("Memories: %v", err)
	}
	if len(mems) != 1 || !strings.Contains(mems[0].Text, "$38") {
		t.Fatalf("the memory did not survive the cache being deleted: %+v", mems)
	}
	hist, err := b2.History("kestrel-one", 5)
	if err != nil || len(hist) != 1 {
		t.Fatalf("the checkpoint did not survive: %v %+v", err, hist)
	}
}

// Retrieval without a model runtime: lexical only, but it must still work.
func TestSearchWithoutAModel(t *testing.T) {
	b, dir := open(t)

	note := "---\ntype: topic\ntitle: BOM cost\n---\nThe waveguide is the single most expensive line item.\n"
	if err := os.WriteFile(filepath.Join(dir, "bom-cost.md"), []byte(note), 0o644); err != nil {
		t.Fatalf("writing a note: %v", err)
	}
	if _, err := b.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if b.Embedded() {
		t.Fatal("WithoutEmbedding still found a model")
	}

	hits, err := b.Search("waveguide expensive", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("lexical search found nothing; the no-model path is broken")
	}
}

func TestContextIsBudgeted(t *testing.T) {
	b, _ := open(t)
	if _, err := b.Checkpoint(brain.Checkpoint{
		Project: "kestrel-one",
		State:   strings.Repeat("a long standing state description. ", 200),
		Next:    "quote the display driver alternatives",
	}); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	c, err := b.Context(brain.Request{Task: "cut the BOM", Project: "kestrel-one", Budget: 200})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	out := c.Render()
	if len(out)/4 > 900 {
		t.Errorf("a 200-token budget produced roughly %d tokens", len(out)/4)
	}
	// The next step is charged before the session log, so a tight budget keeps it.
	if !strings.Contains(out, "display driver") {
		t.Errorf("the budget evicted the next step:\n%s", out)
	}
}
