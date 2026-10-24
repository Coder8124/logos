package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A memory written months ago and recalled today must still read as recalled
// today once the cache is rebuilt. Decay and the fading list both measure from
// the last recall; a rebuild that lost it would offer yesterday's answer as
// one nobody has needed since it was written.
func TestARecallTimeSurvivesDeletingTheIndex(t *testing.T) {
	db, dir := store(t)
	written := time.Now().AddDate(0, -5, 0).Unix()
	if _, err := Store(db, nil, "", &Memory{Text: "the BOM target is $38", Kind: Fact, Source: "test", Created: written}); err != nil {
		t.Fatal(err)
	}
	got, err := Recall(db, nil, "", "BOM target", 3)
	if err != nil || len(got) != 1 {
		t.Fatalf("recall: %v, %d memories", err, len(got))
	}

	db2, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := Init(db2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Import(db2, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	all, err := All(db2)
	if err != nil || len(all) != 1 {
		t.Fatalf("after rebuild: %v, %d memories", err, len(all))
	}
	if since := time.Now().Unix() - all[0].LastUsed; since > 3600 {
		t.Errorf("the rebuilt memory was last recalled %d seconds ago, not today: the recall never reached the vault", since)
	}
	if all[0].Uses != 1 {
		t.Errorf("the rebuilt memory has %d uses, want 1", all[0].Uses)
	}
}

// Once a day is enough for a clock measured in weeks, and a memory's file
// rewritten on every read is churn in a vault people keep in git.
func TestASecondRecallTheSameDayDoesNotRewriteTheMemorysFile(t *testing.T) {
	db, dir := store(t)
	if _, err := Store(db, nil, "", &Memory{Text: "the BOM target is $38", Kind: Fact, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Recall(db, nil, "", "BOM target", 3); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, Dir, string(Fact), "*.md"))
	if len(files) != 1 {
		t.Fatalf("want one memory file, got %v", files)
	}
	before := readFile(t, files[0])
	if _, err := Recall(db, nil, "", "BOM target", 3); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, files[0]); after != before {
		t.Errorf("a second recall within the day rewrote the file:\nbefore %s\nafter  %s", before, after)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
