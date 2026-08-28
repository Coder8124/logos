package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The durability claim in DESIGN.md is that memories are files and the database
// is a cache. These tests attack the seam between the two: what happens when the
// files are there but wrong, or partly there, or written by something else.

func store(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	SetVault(db, dir)
	t.Cleanup(func() { SetVault(db, "") })
	return db, dir
}

func seed(t *testing.T, db *sql.DB, texts map[Kind][]string) {
	t.Helper()
	for kind, list := range texts {
		for _, text := range list {
			if _, err := Store(db, nil, "", &Memory{Text: text, Kind: kind, Source: "test"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func path(dir string, kind Kind) string {
	return filepath.Join(dir, Dir, string(kind)+".md")
}

// One file lost, everything else intact. This is not a hypothetical: a sync
// client restoring a directory, a partial rsync, or a crash between two flushes
// all produce it. The store must not read "this file is gone" as "the user
// deleted all of these".
func TestOneMissingFileDoesNotForgetThatKind(t *testing.T) {
	db, dir := store(t)
	seed(t, db, map[Kind][]string{
		Preference: {"I prefer terse replies"},
		Fact:       {"the BOM target is $38", "the drop test is 1.2m"},
	})

	if err := os.Remove(path(dir, Fact)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}

	facts := kindCount(t, db, Fact)
	if facts != 2 {
		t.Errorf("losing memories/fact.md forgot %d of 2 facts; a missing file is "+
			"absence of information, not an instruction to delete", 2-facts)
	}
	if got := kindCount(t, db, Preference); got != 1 {
		t.Errorf("preference count = %d, want 1", got)
	}
}

// A torn write: something other than brain wrote the file and stopped partway,
// leaving it ending mid-record. Importing it would forget the missing tail.
//
// The cut lands mid-line, which is what an interrupted write actually produces.
// A tear aligned exactly to a line boundary is byte-for-byte what deleting a
// line by hand produces — the documented way to forget something — so it is
// accepted by design. See looksTruncated.
func TestTruncatedFileIsDetected(t *testing.T) {
	db, dir := store(t)
	seed(t, db, map[Kind][]string{
		Fact: {"fact one", "fact two", "fact three", "fact four", "fact five"},
	})

	raw, err := os.ReadFile(path(dir, Fact))
	if err != nil {
		t.Fatal(err)
	}
	// Cut in the middle of the third record's bookkeeping comment.
	idx := nthIndex(string(raw), "<!--", 3)
	if idx < 0 {
		t.Fatal("the store format changed; no third record found")
	}
	torn := string(raw)[:idx+8]
	if err := os.WriteFile(path(dir, Fact), []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, nil, "", dir); err == nil {
		t.Error("a file that ends mid-record was imported without complaint; " +
			"the memories in the missing tail were silently forgotten")
	}
	if got := kindCount(t, db, Fact); got != 5 {
		t.Errorf("%d of 5 facts survived a torn write", got)
	}
}

// Deleting a line by hand is the documented way to forget, and must keep
// working — it is the case a naive truncation check breaks.
func TestDeletingALineStillForgets(t *testing.T) {
	db, dir := store(t)
	seed(t, db, map[Kind][]string{
		Fact: {"the BOM target is $38", "the drop test is 1.2m onto oak"},
	})

	raw, err := os.ReadFile(path(dir, Fact))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "drop test") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path(dir, Fact), []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, nil, "", dir); err != nil {
		t.Fatalf("a hand-deleted line was rejected as damage: %v", err)
	}
	if got := kindCount(t, db, Fact); got != 1 {
		t.Errorf("after deleting one line the store holds %d facts, want 1", got)
	}
}

func nthIndex(s, sub string, n int) int {
	off := 0
	for i := 0; i < n; i++ {
		j := strings.Index(s[off:], sub)
		if j < 0 {
			return -1
		}
		off += j
		if i < n-1 {
			off += len(sub)
		}
	}
	return off
}

// Garbage in the store must not take the process down or wipe the kind.
func TestCorruptStoreIsSurvivable(t *testing.T) {
	for name, content := range map[string]string{
		"binary":            "\x00\x01\x02\xff\xfe not markdown at all",
		"truncated comment": "---\ntype: memory-store\nkind: fact\ncount: 1\n---\n\n- a fact <!-- brain id=1 conf=0.9",
		"bad id":            "---\ntype: memory-store\nkind: fact\ncount: 1\n---\n\n- a fact <!-- brain id=notanumber conf=0.9 -->\n",
		"negative id":       "---\ntype: memory-store\nkind: fact\ncount: 1\n---\n\n- a fact <!-- brain id=-5 conf=0.9 -->\n",
		"huge id":           "---\ntype: memory-store\nkind: fact\ncount: 1\n---\n\n- a fact <!-- brain id=99999999999999999999 conf=0.9 -->\n",
		"crlf":              "---\r\ntype: memory-store\r\nkind: fact\r\ncount: 1\r\n---\r\n\r\n- a fact <!-- brain id=1 conf=0.90 sal=0.50 src=test created=2026-01-01 uses=0 -->\r\n",
		"duplicate ids":     "---\ntype: memory-store\nkind: fact\ncount: 2\n---\n\n- one <!-- brain id=7 -->\n- two <!-- brain id=7 -->\n",
		"no frontmatter":    "- a bare fact somebody typed\n",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			db, dir := store(t)
			if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path(dir, Fact), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Import panicked on %s input: %v", name, r)
				}
			}()
			if _, err := Import(db, nil, "", dir); err != nil {
				t.Logf("Import reported: %v", err)
			}
			if _, err := All(db); err != nil {
				t.Errorf("store unreadable after importing %s: %v", name, err)
			}
		})
	}
}

// A memory written and rebuilt must keep enough of its timestamp to order
// against its neighbours — supersession depends on which of two came later.
func TestCreatedTimeSurvivesTheRoundTrip(t *testing.T) {
	db, dir := store(t)

	morning := time.Date(2026, 3, 2, 9, 15, 0, 0, time.UTC).Unix()
	evening := time.Date(2026, 3, 2, 18, 40, 0, 0, time.UTC).Unix()
	for _, m := range []Memory{
		{Text: "the target is $42", Kind: Fact, Source: "test", Created: morning},
		{Text: "the target is $38", Kind: Fact, Source: "test", Created: evening},
	} {
		mm := m
		if _, err := Store(db, nil, "", &mm); err != nil {
			t.Fatal(err)
		}
	}

	db2, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := Init(db2); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(db2, nil, "", dir); err != nil {
		t.Fatal(err)
	}

	all, err := All(db2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 memories, got %d", len(all))
	}
	var early, late Memory
	for _, m := range all {
		if strings.Contains(m.Text, "$42") {
			early = m
		} else {
			late = m
		}
	}
	if !(early.Created < late.Created) {
		t.Errorf("after a rebuild the two are no longer ordered in time: %d vs %d "+
			"(the file records a date, not a timestamp)", early.Created, late.Created)
	}
}

// The user's own edits are the whole promise of a file-backed store.
func TestHandEditsSurviveAtScale(t *testing.T) {
	db, dir := store(t)
	const n = 2000
	for i := 0; i < n; i++ {
		if _, err := Store(db, nil, "", &Memory{
			Text: fmt.Sprintf("fact number %d about the project", i),
			Kind: Fact, Source: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(path(dir, Fact))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "fact number 7 about", "CORRECTED fact number 7 about", 1)
	if edited == string(raw) {
		t.Fatal("the edit did not apply; the file format changed")
	}
	if err := os.WriteFile(path(dir, Fact), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(db, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	if got := kindCount(t, db, Fact); got != n {
		t.Errorf("after a hand edit the store holds %d of %d", got, n)
	}
	var text string
	if err := db.QueryRow("SELECT text FROM memories WHERE text LIKE 'CORRECTED%'").Scan(&text); err != nil {
		t.Errorf("the correction did not stick: %v", err)
	}
}

func kindCount(t *testing.T, db *sql.DB, kind Kind) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM memories WHERE kind = ? AND superseded = 0", string(kind),
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
