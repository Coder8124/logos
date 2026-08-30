package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The indexer walks a directory the user controls and that other tools write
// into. Everything here is something a real vault eventually contains.

func openVault(t *testing.T) (*Index, string) {
	t.Helper()
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix, dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A vault is a folder. People put things in folders.
func TestSyncSurvivesAPathologicalVault(t *testing.T) {
	ix, dir := openVault(t)

	write(t, dir, "good.md", "---\ntype: topic\ntitle: Good\n---\nreal content here\n")
	write(t, dir, "binary.md", "\x00\x01\x02\xff\xfe\x00 not text at all")
	write(t, dir, "empty.md", "")
	write(t, dir, "only-frontmatter.md", "---\ntype: topic\n---\n")
	write(t, dir, "broken-fm.md", "---\nrelations: [this: is: not: yaml\n---\nbody\n")
	write(t, dir, "unicode-世界-🚀.md", "---\ntype: topic\ntitle: 世界 🚀\n---\nCJK and emoji in the title\n")
	write(t, dir, "rtl-מסמך.md", "---\ntype: topic\ntitle: מסמך\n---\nright to left\n")
	write(t, dir, "crlf.md", "---\r\ntype: topic\r\ntitle: CRLF\r\n---\r\nwindows line endings\r\n")
	write(t, dir, "nested/deep/deeper/note.md", "---\ntype: topic\n---\nnested\n")

	// Same basename in two directories: do the slugs collide?
	write(t, dir, "people/sam.md", "---\ntype: person\ntitle: Sam A\n---\nfirst sam\n")
	write(t, dir, "topics/sam.md", "---\ntype: topic\ntitle: Sam B\n---\nsecond sam\n")

	// A symlink pointing outside the vault, and one that loops.
	outside := filepath.Join(t.TempDir(), "outside.md")
	os.WriteFile(outside, []byte("---\ntype: topic\n---\nshould not be indexed\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "link-out.md")); err != nil {
		t.Logf("symlink unsupported: %v", err)
	}
	os.Symlink(dir, filepath.Join(dir, "loop"))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Sync panicked on a pathological vault: %v", r)
		}
	}()
	rep, err := ix.Sync()
	if err != nil {
		t.Fatalf("one bad file aborted the whole index: %v", err)
	}
	t.Logf("sync report: %+v", rep)

	hits, err := ix.LexicalSearch("real content", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the good note was not indexed alongside the bad ones")
	}

	var sams int
	if err := ix.DB.QueryRow("SELECT COUNT(*) FROM notes WHERE slug LIKE '%sam%'").Scan(&sams); err != nil {
		t.Fatal(err)
	}
	if sams != 2 {
		t.Errorf("people/sam.md and topics/sam.md produced %d notes, want 2 — "+
			"same-basename files in different folders collide", sams)
	}

	var leaked int
	ix.DB.QueryRow("SELECT COUNT(*) FROM notes WHERE body LIKE '%should not be indexed%'").Scan(&leaked)
	if leaked > 0 {
		t.Error("a symlink pulled a file from outside the vault into the index")
	}
}

// FTS5 treats punctuation as syntax. Every one of these is something a user or
// an agent will eventually type into ask or search.
func TestHostileQueriesDoNotError(t *testing.T) {
	ix, dir := openVault(t)
	write(t, dir, "note.md", "---\ntype: topic\ntitle: BOM\n---\nthe waveguide costs $4.20\n")
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{
		``, `   `, `"`, `""`, `*`, `**`, `NEAR`, `OR`, `OR OR OR`, `AND`,
		`a OR`, `(unbalanced`, `slug:*`, `"unterminated`, `^`, `-`, `--`,
		`NEAR(a b`, `$4.20`, `世界`, `🚀`, `\x00`, strings.Repeat("waveguide ", 20000),
	} {
		t.Run(fmt.Sprintf("%.20q", q), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("query %q panicked: %v", q, r)
				}
			}()
			if _, err := ix.LexicalSearch(q, 5); err != nil {
				t.Errorf("query %q errored: %v", q, err)
			}
		})
	}
}

// A note big enough to matter. Sync reads whole files into memory.
func TestOversizeNote(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates ~64MB")
	}
	ix, dir := openVault(t)

	big := "---\ntype: topic\ntitle: Huge\n---\n" + strings.Repeat("lorem ipsum dolor sit amet ", 2_500_000)
	write(t, dir, "huge.md", big)
	write(t, dir, "small.md", "---\ntype: topic\ntitle: Small\n---\nfindable\n")

	if _, err := ix.Sync(); err != nil {
		t.Fatalf("a 65MB note broke the index: %v", err)
	}
	hits, err := ix.LexicalSearch("findable", 5)
	if err != nil || len(hits) == 0 {
		t.Errorf("the small note became unfindable next to a huge one: %v", err)
	}
}

// Deleting a note must remove it from every derived table, not just notes.
func TestDeletionLeavesNoOrphans(t *testing.T) {
	ix, dir := openVault(t)
	write(t, dir, "gone.md", "---\ntype: topic\ntitle: Gone\nrelations:\n  - { pred: relates_to, obj: \"[[other]]\", conf: 1.0, src: stated }\n---\nbody\n")
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{
		"SELECT COUNT(*) FROM notes WHERE slug = 'gone'",
		"SELECT COUNT(*) FROM notes_fts WHERE slug = 'gone'",
		"SELECT COUNT(*) FROM edges WHERE src_slug = 'gone'",
		"SELECT COUNT(*) FROM aliases WHERE slug = 'gone'",
		"SELECT COUNT(*) FROM embeddings WHERE slug = 'gone'",
	} {
		var n int
		if err := ix.DB.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d orphaned rows after deletion: %s", n, q)
		}
	}
}

// Reopening a database left behind by a killed process must recover the WAL.
func TestReopenAfterUncleanClose(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.md", "---\ntype: topic\ntitle: A\n---\nalpha\n")
	if _, err := ix.Sync(); err != nil {
		t.Fatal(err)
	}
	// Drop the handle without closing, as a killed process does.
	ix.DB.SetMaxOpenConns(1)
	ix = nil

	ix2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopening after an unclean close failed: %v", err)
	}
	defer ix2.Close()
	hits, err := ix2.LexicalSearch("alpha", 5)
	if err != nil || len(hits) == 0 {
		t.Errorf("data written before an unclean close was not recovered: %v", err)
	}
}

// One file the process cannot read must cost that one file, not the index.
//
// The walk deliberately tolerates unreadable entries ("unreadable entries must
// not abort the whole walk"), but the read inside the loop does not — so a
// single permission-denied file, or a file another process is holding, stops
// the whole sync and every other note goes unindexed.
func TestUnreadableFileDoesNotAbortTheIndex(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permissions are not enforced")
	}
	ix, dir := openVault(t)

	write(t, dir, "readable.md", "---\ntype: topic\ntitle: Readable\n---\nfindable content\n")
	write(t, dir, "locked.md", "---\ntype: topic\n---\nsecret\n")
	locked := filepath.Join(dir, "locked.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot make a file unreadable here: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })

	if _, err := ix.Sync(); err != nil {
		t.Fatalf("one unreadable file aborted the entire index: %v", err)
	}
	hits, err := ix.LexicalSearch("findable", 5)
	if err != nil || len(hits) == 0 {
		t.Error("the readable note went unindexed because of an unreadable neighbour")
	}
}

// A dangling symlink is what a half-finished sync client leaves behind.
func TestDanglingSymlinkDoesNotAbortTheIndex(t *testing.T) {
	ix, dir := openVault(t)
	write(t, dir, "real.md", "---\ntype: topic\ntitle: Real\n---\nfindable content\n")

	target := filepath.Join(t.TempDir(), "vanished.md")
	if err := os.Symlink(target, filepath.Join(dir, "dangling.md")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	if _, err := ix.Sync(); err != nil {
		t.Fatalf("a dangling symlink aborted the index: %v", err)
	}
	if hits, err := ix.LexicalSearch("findable", 5); err != nil || len(hits) == 0 {
		t.Error("the real note went unindexed because of a dangling symlink")
	}
}
