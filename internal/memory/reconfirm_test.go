package memory

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `logos review --stale` answers a drift warning by moving the memory's commit
// to head. Kept only in the index, the answer would be lost with it and every
// warning a person had answered would come back after `logos index`.
func TestAReconfirmedCommitSurvivesDeletingTheIndex(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "internal/parse/reader.go is the only reader of the export", Kind: Fact, Source: "manual", Commit: "aaaaaaa"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if err := Reconfirm(db, m.ID, "bbbbbbb"); err != nil {
		t.Fatal(err)
	}

	wiped := testDB(t)
	if _, _, err := Import(wiped, nil, "", dir); err != nil {
		t.Fatal(err)
	}
	got, err := All(wiped)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Commit != "bbbbbbb" {
		t.Fatalf("the re-confirmed commit did not survive a reindex: %+v", got)
	}
}

func TestReconfirmingAMemoryThatDoesNotExistSaysSo(t *testing.T) {
	db, _ := vaultDB(t)
	if err := Reconfirm(db, 999, "bbbbbbb"); err == nil {
		t.Fatal("re-confirming a memory nobody stored reported success")
	}
}

// The vault is truth: a re-confirm whose file could not be written must not
// leave the index saying it happened, or the answer is lost on the next
// `logos index` while the person was told it was kept.
func TestAReconfirmTheVaultRefusedLeavesTheIndexAsItWas(t *testing.T) {
	db, dir := vaultDB(t)
	m := Memory{Text: "internal/parse/reader.go is the only reader of the export", Kind: Fact, Source: "manual", Commit: "aaaaaaa"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, filepath.Dir(p))
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("the memory was stored without a vault file")
	}
	for _, d := range files {
		os.Chmod(d, 0o500)
		t.Cleanup(func() { os.Chmod(d, 0o700) })
	}

	if err := Reconfirm(db, m.ID, "bbbbbbb"); err == nil {
		t.Fatal("a re-confirm the vault refused reported success")
	}
	got, err := All(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Commit != "aaaaaaa" {
		t.Fatalf("the index kept a re-confirm the vault never got: %+v", got)
	}
}
