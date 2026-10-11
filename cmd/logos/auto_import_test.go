package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/memory"
)

// claudeMemAt points claude-mem's data directory at a fixture store and counts
// the imports the switch launches, without a second process.
func claudeMemAt(t *testing.T, rows [][4]string) (string, *int) {
	t.Helper()
	path := claudeMemFixture(t, rows)
	// Written a minute ago: in a real session claude-mem's last write is not
	// in the same second as the resume that notices it.
	touch(t, path, time.Now().Add(-time.Minute))
	t.Setenv("CLAUDE_MEM_DATA_DIR", filepath.Dir(path))
	t.Setenv("LOGOS_EMBED", "off")
	launched := 0
	old := auto.Launch
	auto.Launch = func(_ string, args ...string) error {
		if len(args) > 0 && args[0] == "import" {
			launched++
		}
		return nil
	}
	t.Cleanup(func() { auto.Launch = old })
	return path, &launched
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

var twoObservations = [][4]string{
	{"kestrel", "Waveguide supplier quotes 4.20 per unit", "", ""},
	{"kestrel", "Staging runs on port 9090", "", ""},
}

func TestTheImportSwitchOffNeverStartsAnImport(t *testing.T) {
	standIn(t, "shop")
	_, launched := claudeMemAt(t, twoObservations)
	resumeShop(t)
	if *launched != 0 {
		t.Fatalf("an import was launched with the switch off")
	}
}

// The import is started once per change to claude-mem's store, says so when it
// is, and the resume after the agent's checkpoint still reads out what it
// brought in — the order a real session puts these in.
func TestTheImportSwitchImportsWhatIsNewOnceAndResumeSaysHowMany(t *testing.T) {
	standIn(t, "shop")
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	db, launched := claudeMemAt(t, twoObservations)
	if err := auto.Set(vaultPath(), string(auto.Import), true); err != nil {
		t.Fatal(err)
	}

	if out := resumeShop(t); !strings.Contains(out, "claude-mem changed; importing what is new") {
		t.Errorf("resume started an import without saying so:\n%s", out)
	}
	if err := runImportAuto(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // the checkpoint is stamped to the second
	if err := runCheckpoint([]string{"shop", "--task", "fix the cache", "--next", "ship it"}); err != nil {
		t.Fatal(err)
	}
	if out := resumeShop(t); !strings.Contains(out, "imported 2 memories from claude-mem") {
		t.Errorf("resume did not say what the import brought in:\n%s", out)
	}
	if *launched != 1 {
		t.Errorf("an unchanged store was imported again: %d launches, want 1", *launched)
	}
	if got := len(importedMemories(t)); got != 2 {
		t.Errorf("the import left %d memories, want 2", got)
	}

	touch(t, db, time.Now().Add(time.Minute))
	resumeShop(t)
	if *launched != 2 {
		t.Errorf("claude-mem wrote again and %d imports were launched in all, want 2", *launched)
	}
}

// Forgetting an imported memory is the user overruling the import. The next
// time claude-mem writes, the switch must not put it back — nor after the
// index is deleted, which is when a record kept only in SQLite would be lost.
func TestAForgottenImportIsNotBroughtBackByTheImportSwitch(t *testing.T) {
	standIn(t, "shop")
	db, _ := claudeMemAt(t, twoObservations)
	if err := auto.Set(vaultPath(), string(auto.Import), true); err != nil {
		t.Fatal(err)
	}
	importFrom(t, db)
	var gone int64
	for _, m := range importedMemories(t) {
		if strings.HasPrefix(m.Text, "Staging") {
			gone = m.ID
		}
	}
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Forget(ix.DB, gone); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	deleteIndex(t)

	n, err := importAuto()
	if err != nil {
		t.Fatal(err)
	}
	if n.imported != 0 || n.present != 2 {
		t.Errorf("the switch imported %d and skipped %d, want 0 and 2", n.imported, n.present)
	}
	for _, m := range importedMemories(t) {
		if strings.HasPrefix(m.Text, "Staging") {
			t.Fatalf("the forgotten memory came back: %q", m.Text)
		}
	}
}
