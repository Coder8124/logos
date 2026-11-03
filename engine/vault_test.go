package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI's index names a memory file it kept but could not read; an embedder
// calling Index got the same file kept and frozen with no word of it, because
// only the conflict copies were passed on.
func TestIndexNamesAMemoryFileItKeptButCouldNotRead(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir, WithoutEmbedding())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Remember("deploys go through the staging gate", Fact); err != nil {
		t.Fatal(err)
	}
	kindDir := filepath.Join(dir, "memories", "fact")
	entries, err := os.ReadDir(kindDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want one memory file, got %v (%v)", entries, err)
	}
	path := filepath.Join(kindDir, entries[0].Name())
	raw, _ := os.ReadFile(path)
	kept := strings.SplitN(string(raw), "\n- ", 2)[0] + "\n"
	if err := os.WriteFile(path, []byte(kept), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := b.Index()
	if err != nil {
		t.Fatal(err)
	}
	want := "memories/fact/" + entries[0].Name()
	for _, u := range rep.Unreadable {
		if strings.HasPrefix(u, want) {
			return
		}
	}
	t.Errorf("Unreadable = %v, want %s named", rep.Unreadable, want)
}
