package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/contextpack"
)

func seedTreeVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memories/preference.md", "- terse replies\n")
	write("sessions/kestrel-one/20260101-000000-claude.md", "---\ntype: checkpoint\n---\nbody\n")
	write("topics/bom-cost.md", "---\ntype: note\ntitle: BOM cost\n---\nbody\n")
	write("ingest/candidate-1.md", "candidate\n")
	return dir
}

// The tree is a live view of the vault, not a second inventory of it — a file
// that exists on disk with no entry in the tree would be a place a pin/exclude
// or an edit silently could not reach.
func TestVaultTreeListsRealFilesFromEveryVaultDirectory(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	nodes, err := a.VaultTree()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"memories": false, "sessions": false, "topics": false, "ingest": false}
	for _, n := range nodes {
		if _, ok := want[n.Name]; ok {
			want[n.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("expected a top-level node named %q, got %v", name, nodes)
		}
	}
}

// The dot-directories that back this very feature (.brain, .context) must
// never show up as nodes — a rules file editable through the tree it drives
// would be a promise this code cannot keep (editing it could desync the tree
// from the rule the tree itself just wrote).
func TestVaultTreeNeverListsADotDirectory(t *testing.T) {
	dir := seedTreeVault(t)
	if err := contextpack.SetPathRule(dir, "topics/bom-cost.md", contextpack.PathPinAlways); err != nil {
		t.Fatal(err)
	}
	a := NewApp(dir)
	nodes, err := a.VaultTree()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if strings.HasPrefix(n.Name, ".") {
			t.Fatalf("dot-directory leaked into the tree: %v", n)
		}
	}
}

// A node's Pin field is the same rule contextpack.Build enforces, read
// straight from the rules file — the tree has to agree with what the pack
// actually does, not keep its own opinion about it.
func TestVaultTreeAnnotatesANodeThatWasPinnedThroughSetTreePin(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	if err := a.SetTreePin("topics/bom-cost.md", "pin"); err != nil {
		t.Fatal(err)
	}
	nodes, err := a.VaultTree()
	if err != nil {
		t.Fatal(err)
	}
	var topics *TreeNode
	for i := range nodes {
		if nodes[i].Name == "topics" {
			topics = &nodes[i]
		}
	}
	if topics == nil {
		t.Fatal("expected a topics node")
	}
	var found bool
	for _, c := range topics.Children {
		if c.Name == "bom-cost.md" {
			found = true
			if c.Pin != "pin" {
				t.Fatalf("expected pin state %q, got %q", "pin", c.Pin)
			}
		}
	}
	if !found {
		t.Fatal("expected a bom-cost.md node under topics")
	}
}

// Clearing a rule (mode "") must make the node read exactly like one that was
// never pinned — the tree's "clear" control has to be a real inverse of "pin",
// not a state that lingers as some third thing.
func TestClearingATreePinRemovesItFromTheRulesFile(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	if err := a.SetTreePin("topics/bom-cost.md", "exclude"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetTreePin("topics/bom-cost.md", ""); err != nil {
		t.Fatal(err)
	}
	rules, err := contextpack.LoadPathRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected the rule to be gone, got %v", rules)
	}
}

// The edit pane's whole promise ("edit any line to correct it") depends on
// read-then-write round-tripping the exact bytes on disk.
func TestReadingThenWritingAVaultFileRoundTripsTheEditedContent(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	got, err := a.ReadVaultFile("topics/bom-cost.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "BOM cost") {
		t.Fatalf("did not read back the seeded content: %q", got)
	}
	edited := got + "\nan added line\n"
	if err := a.WriteVaultFile("topics/bom-cost.md", edited); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "topics/bom-cost.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != edited {
		t.Fatalf("file on disk does not match what was saved:\ngot:  %q\nwant: %q", raw, edited)
	}
}

// A path that climbs out of the vault ("../../etc/passwd"-shaped) must be
// refused rather than resolved — the tree only ever hands this function paths
// it produced itself, but the function must not trust that.
func TestWritingAPathThatEscapesTheVaultIsRefused(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	if err := a.WriteVaultFile("../outside.md", "nope"); err == nil {
		t.Fatal("expected an error, got none")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "outside.md")); err == nil {
		t.Fatal("the escaping write actually landed on disk")
	}
}

// A saved edit has to be visible to the next context pack, the same reindex
// discipline every other vault write in this codebase follows — a save that
// only touched the file but left the cache stale would make the preview panel
// beside the edit pane lie about what was just typed.
func TestSavingAnEditedFileMakesItSearchableAgain(t *testing.T) {
	dir := seedTreeVault(t)
	a := NewApp(dir)
	edited := "---\ntype: note\ntitle: BOM cost\n---\nnewly distinctive marker phrase zzyzx\n"
	if err := a.WriteVaultFile("topics/bom-cost.md", edited); err != nil {
		t.Fatal(err)
	}
	ix, err := a.open()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	hits, err := ix.LexicalSearch("zzyzx", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("edited content was not reindexed after WriteVaultFile")
	}
}
