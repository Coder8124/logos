package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/memory"
)

// A rule file that never existed must read back as "no rules", not an error —
// a vault nobody has pinned or excluded anything in is the common case, and
// treating its absence as a failure would make every cold Build error out.
func TestLoadingPathRulesFromAVaultWithNoneYetReturnsAnEmptySlice(t *testing.T) {
	dir := t.TempDir()
	rules, err := LoadPathRules(dir)
	if err != nil {
		t.Fatalf("missing rules file must not be an error: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected no rules, got %v", rules)
	}
}

// The whole point of storing this as markdown rather than a database row: it
// must survive being deleted-and-rebuilt the same way every other durable
// vault feature does. There is nothing to rebuild it *from* except itself, so
// the test that matters is the round trip through the file, not through SQL.
func TestAPinnedPathSurvivesReadingItBackFromTheVault(t *testing.T) {
	dir := t.TempDir()
	if err := SetPathRule(dir, "sessions/kestrel-one", PathPinAlways); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadPathRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Prefix != "sessions/kestrel-one" || rules[0].Pin != PathPinAlways {
		t.Fatalf("got %v", rules)
	}

	// And the promise every hand-edited vault file makes: it is plain text,
	// editable and deletable by a human, not an opaque blob.
	raw, err := os.ReadFile(RulesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pin: sessions/kestrel-one") {
		t.Fatalf("rule is not readable prose:\n%s", raw)
	}
}

// Re-pinning the same path must update the one line, not append a second,
// contradictory one that leaves it ambiguous which rule wins.
func TestSettingTheSamePathTwiceReplacesTheRuleRatherThanDuplicatingIt(t *testing.T) {
	dir := t.TempDir()
	if err := SetPathRule(dir, "notes/design", PathPinAlways); err != nil {
		t.Fatal(err)
	}
	if err := SetPathRule(dir, "notes/design", PathPinNever); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadPathRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Pin != PathPinNever {
		t.Fatalf("expected exactly one exclude rule, got %v", rules)
	}
}

// Clearing a rule (PathPinNone) must remove the line entirely — a path with no
// opinion recorded about it should read exactly like a vault where it was
// never mentioned.
func TestClearingAPathRuleRemovesItFromTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := SetPathRule(dir, "ingest/some-transcript", PathPinNever); err != nil {
		t.Fatal(err)
	}
	if err := SetPathRule(dir, "ingest/some-transcript", PathPinNone); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadPathRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected the rule to be gone, got %v", rules)
	}
}

// The nested rule is more specific and must win over the broader one it sits
// inside, the same way a file manager's per-folder override behaves — without
// this, excluding one stale project under sessions/ would be impossible to
// combine with pinning sessions/ as a whole.
func TestAMoreSpecificPathRuleOverridesABroaderOne(t *testing.T) {
	rules := []PathRule{
		{Prefix: "sessions", Pin: PathPinAlways},
		{Prefix: "sessions/scratch", Pin: PathPinNever},
	}
	if got := matchPathRule("sessions/scratch/notes", rules); got != PathPinNever {
		t.Fatalf("expected the narrower exclude to win, got %v", got)
	}
	if got := matchPathRule("sessions/kestrel-one/checkpoint", rules); got != PathPinAlways {
		t.Fatalf("expected the broader pin to apply outside the excluded subtree, got %v", got)
	}
}

// A prefix must not match a sibling directory that merely shares its leading
// characters — "sessions" pinning "sessions-archive" would silently pull in a
// project's whole history because of a naming coincidence.
func TestAPathRuleDoesNotMatchASiblingWithASharedPrefix(t *testing.T) {
	rules := []PathRule{{Prefix: "sessions", Pin: PathPinAlways}}
	if got := matchPathRule("sessions-archive/old", rules); got != PathPinNone {
		t.Fatalf("expected no match against a sibling directory, got %v", got)
	}
}

// This is the property the tree view actually depends on: excluding a memory
// kind's file must remove every memory of that kind from every section of the
// pack, and pinning one must force every memory of that kind in — a folder
// toggle acting exactly like the per-memory Pin field it is built on top of.
func TestExcludingAMemoryKindsFileDropsEveryMemoryOfThatKindFromThePack(t *testing.T) {
	ix := seedVault(t)
	_, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: "likes terse replies", Kind: memory.Preference, Salience: 0.9, Confidence: 0.9, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}

	if err := SetPathRule(ix.Vault, memoryPathFor(memory.Preference), PathPinNever); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Preferences {
		if m.Kind == memory.Preference {
			t.Fatalf("preference memory should have been excluded by its folder rule: %v", m)
		}
	}
}

func TestPinningAMemoryKindsFileForcesEveryMemoryOfThatKindIntoPinned(t *testing.T) {
	ix := seedVault(t)
	receipt, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: "the client is ÉlyséeBot", Kind: memory.Person, Salience: 0.4, Confidence: 0.9, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	id := receipt.ID

	if err := SetPathRule(ix.Vault, memoryPathFor(memory.Person), PathPinAlways); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range p.Pinned {
		if m.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("person memory should have been forced into Pinned by its folder rule, got %v", p.Pinned)
	}
}

// Excluding a note path must remove it from the pack even though it was
// reached through the graph, not just direct search — the tree view's
// exclude has to be the last word, after every retrieval arm has run.
func TestExcludingANotePathDropsItEvenWhenTheGraphWouldHavePulledItIn(t *testing.T) {
	ix := seedVault(t)

	if err := SetPathRule(ix.Vault, "topics/yield-rate", PathPinNever); err != nil {
		t.Fatal(err)
	}

	p, err := Build(ix, nil, "", Request{Task: "reduce the bill of materials", Hint: "kestrel-one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Notes {
		if strings.Contains(h.Slug, "yield-rate") {
			t.Fatalf("yield-rate should have been excluded, got it via=%q", h.Via)
		}
	}
}

// Pinning a note path must surface it even when nothing in the task or the
// graph would otherwise have found it — the tree view's whole promise is that
// checking a box is enough, independent of ranking.
func TestPinningANotePathSurfacesItEvenWithNoQueryMatch(t *testing.T) {
	ix := seedVault(t)

	if err := SetPathRule(ix.Vault, "topics/yield-rate", PathPinAlways); err != nil {
		t.Fatal(err)
	}

	// No embedder and a task with none of yield-rate's words: ordinary
	// retrieval (nil embed skips HybridSearch) and the graph (no Hint) both
	// have nothing to do with it.
	p, err := Build(ix, nil, "", Request{Task: "unrelated question"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, h := range p.Notes {
		if strings.Contains(h.Slug, "yield-rate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned note should have been forced in, got %v", slugs(p.Notes))
	}
}

func TestRulesFileLivesUnderADotDirectorySoItIsNeverIndexedAsANote(t *testing.T) {
	dir := t.TempDir()
	if err := SetPathRule(dir, "notes/anything", PathPinAlways); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(dir, RulesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, ".") {
		t.Fatalf("rules file must live under a dot-directory, got %s", rel)
	}
}
