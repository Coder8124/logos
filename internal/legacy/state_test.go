package legacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.4 kept its index, model config and ingest consent in .brain/. Unmoved, an
// upgraded vault lost its config and consent without a word and rebuilt the
// index from nothing beside the old one.
func TestTheOldStateDirectoryIsMovedToItsNewName(t *testing.T) {
	v := t.TempDir()
	if err := os.MkdirAll(filepath.Join(v, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v, ".brain", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	notice := StateDir(v)

	if _, err := os.Stat(filepath.Join(v, ".logos", "config.json")); err != nil {
		t.Errorf("config.json is not under .logos after start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v, ".brain")); !os.IsNotExist(err) {
		t.Errorf(".brain is still there after the move: %v", err)
	}
	if !strings.Contains(notice, ".brain") || !strings.Contains(notice, ".logos") {
		t.Errorf("start did not say it moved .brain to .logos: %q", notice)
	}
}

// With both present, either could hold the config someone relies on, and a
// merge would pick for them. Say so and touch neither.
func TestBothStateDirectoriesAreLeftAloneWithAWarning(t *testing.T) {
	v := t.TempDir()
	for _, d := range []string{".brain", ".logos"} {
		if err := os.Mkdir(filepath.Join(v, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	notice := StateDir(v)

	if _, err := os.Stat(filepath.Join(v, ".brain")); err != nil {
		t.Errorf(".brain was touched: %v", err)
	}
	if !strings.Contains(notice, ".brain") || !strings.Contains(notice, "not read") {
		t.Errorf("start did not warn that .brain is left over and not read: %q", notice)
	}
}

func TestAVaultWithoutTheOldStateDirectoryIsNotMentioned(t *testing.T) {
	if notice := StateDir(t.TempDir()); notice != "" {
		t.Errorf("start announced %q on a vault with no .brain", notice)
	}
}
