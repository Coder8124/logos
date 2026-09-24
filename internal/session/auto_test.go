package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A record someone took on as their own — the auto marker removed, a next step
// written in — is theirs now. The sweep bringing it up to date must not put a
// mechanical file list back over what they wrote.
func TestAnAutoRecordSomebodyMadeTheirOwnIsNotRewritten(t *testing.T) {
	vault := t.TempDir()
	old, err := WriteAuto(vault, Checkpoint{Project: "shop", Agent: "cursor", Files: []string{"cart.go"}, TS: time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, filepath.FromSlash(old.Slug)+".md")
	raw, _ := os.ReadFile(path)
	reviewed := strings.Replace(string(raw), "auto: true\n", "", 1)
	if reviewed == string(raw) {
		t.Fatal("the record has no auto marker to remove")
	}
	if err := os.WriteFile(path, []byte(reviewed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := GrowAuto(vault, old, Checkpoint{Files: []string{"cart.go", "refund.go"}}); err == nil {
		t.Error("a record no longer marked auto was rewritten without complaint")
	}
	if after, _ := os.ReadFile(path); string(after) != reviewed {
		t.Errorf("the reviewed record was changed:\n%s", after)
	}
}

// Growing a record keeps where it sits in the chain: whatever it followed, it
// still follows, and it stays in the file others already link to.
func TestAGrownAutoRecordKeepsItsFileAndWhatItFollows(t *testing.T) {
	vault := t.TempDir()
	first, err := WriteAuto(vault, Checkpoint{Project: "shop", Agent: "cursor", Files: []string{"a.go"}, TS: time.Now().Add(-2 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	old, err := WriteAuto(vault, Checkpoint{Project: "shop", Agent: "cursor", Files: []string{"b.go"}, TS: time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	grown, err := GrowAuto(vault, old, Checkpoint{Files: []string{"b.go", "c.go"}, TS: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if grown.Slug != old.Slug {
		t.Errorf("the record moved from %s to %s", old.Slug, grown.Slug)
	}
	raw, _ := os.ReadFile(filepath.Join(vault, filepath.FromSlash(old.Slug)+".md"))
	if !strings.Contains(string(raw), "[["+first.Session+"]]") || !strings.Contains(string(raw), "c.go") {
		t.Errorf("the grown record lost what it follows or the new work:\n%s", raw)
	}
}
