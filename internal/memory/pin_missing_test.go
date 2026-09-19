package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invariant 4: a failure is reported, never swallowed. setPin ignored the
// lookup error, so pinning or excluding an id nobody ever created answered
// "Pinned — always included in context packs, budget permitting." An agent
// that mistypes an id, or reuses one from another vault or a stale
// list_memories, is told the pin worked, and then the user wonders for the
// rest of the session why the fact never surfaces.
func TestPinningAMemoryThatDoesNotExistIsAnError(t *testing.T) {
	db, _ := vaultDB(t)

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"pin", func() error { return Pin(db, 999) }},
		{"unpin", func() error { return Unpin(db, 999) }},
		{"exclude", func() error { return Exclude(db, 999) }},
	} {
		err := tc.call()
		if err == nil {
			t.Errorf("%s on a memory that does not exist reported success", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "999") {
			t.Errorf("the %s error does not name the id: %v", tc.name, err)
		}
	}
}

// The phantom is not only in the answer: it went into the vault's
// append-only log, where it stays for good. `logos memory log` showed
// "updated #999 pinned:" with empty text, filed under the empty kind.
func TestPinningAMemoryThatDoesNotExistWritesNothingToTheLog(t *testing.T) {
	db, dir := vaultDB(t)

	_ = Pin(db, 999)
	_ = Exclude(db, 999)

	raw, err := os.ReadFile(filepath.Join(dir, Dir, LogFile))
	if err != nil {
		if os.IsNotExist(err) {
			return // nothing written at all is the strongest form of the same claim
		}
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "999") {
		t.Errorf("the vault's log records an event for a memory that never existed:\n%s", raw)
	}
}
