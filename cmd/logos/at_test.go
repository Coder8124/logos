package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
)

// --at dates a record to when it happened. The date has to live in the
// markdown, or it is a cache-only fact that `rm -rf .logos && logos index`
// quietly turns back into "today" (invariant 1).
func TestARecordDatedWithAtKeepsItsDateAfterTheIndexIsDeleted(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")

	noteAt := time.Date(2026, 3, 2, 9, 30, 0, 0, time.Local).Unix()
	memAt := time.Date(2026, 3, 4, 11, 0, 0, 0, time.Local).Unix()
	cpAt := time.Date(2026, 3, 6, 17, 15, 0, 0, time.Local).Unix()

	said := captureStdout(t, func() {
		// Another project, so the checkpoint below does not commit the note.
		if err := runNote([]string{"harrier", "--at", "2026-03-02T09:30", "tried the 3.3V rail, it browned out"}); err != nil {
			t.Fatalf("note: %v", err)
		}
		if err := memoryCmd([]string{"add", "--project", "kestrel", "--at", "2026-03-04T11:00", "the waveguide costs 4.20 dollars"}); err != nil {
			t.Fatalf("memory add: %v", err)
		}
		if err := runCheckpoint([]string{"--task", "quote the waveguide", "--at", "2026-03-06T17:15"}); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
	})
	if n := strings.Count(said, "Dated 2026-03-0"); n != 3 {
		t.Errorf("each backdated write should say its date in the receipt, %d did:\n%s", n, said)
	}

	if err := os.RemoveAll(filepath.Join(vaultDir, ".logos")); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(false); err != nil {
		t.Fatal(err)
	}

	c, err := session.Latest(vaultPath(), "kestrel")
	if err != nil || c == nil {
		t.Fatalf("no checkpoint after the rebuild: %v", err)
	}
	if c.TS != cpAt {
		t.Errorf("checkpoint dated %v, want %v", time.Unix(c.TS, 0), time.Unix(cpAt, 0))
	}

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	notes, err := session.Uncommitted(ix.DB, "harrier")
	if err != nil || len(notes) != 1 {
		t.Fatalf("want the one note after the rebuild, got %v (%v)", notes, err)
	}
	if notes[0].TS != noteAt || strings.Contains(notes[0].Text, "--at") {
		t.Errorf("note came back as %q dated %v, want its words alone dated %v", notes[0].Text, time.Unix(notes[0].TS, 0), time.Unix(noteAt, 0))
	}
	mems, err := memory.All(ix.DB)
	if err != nil || len(mems) != 1 {
		t.Fatalf("want the one memory after the rebuild, got %d (%v)", len(mems), err)
	}
	if mems[0].Created != memAt || strings.Contains(mems[0].Text, "--at") {
		t.Errorf("memory came back as %q dated %v, want its words alone dated %v", mems[0].Text, time.Unix(mems[0].Created, 0), time.Unix(memAt, 0))
	}
}

// A future date would sort after every real write for as long as the vault
// exists, so a typo in the year would supersede everything after it.
func TestAtRefusesATimeInTheFutureAndOneItCannotRead(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)
	for _, v := range []string{"2027-01-01", "tuesday", ""} {
		if _, err := parseAt(v, now); err == nil {
			t.Errorf("--at %q was accepted", v)
		}
	}
	for v, want := range map[string]int64{
		"2026-11-03":           time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local).Unix(),
		"2026-11-03T14:05":     time.Date(2026, 11, 3, 14, 5, 0, 0, time.Local).Unix(),
		"2026-11-03T14:05:00Z": time.Date(2026, 11, 3, 14, 5, 0, 0, time.UTC).Unix(),
		"1762000000":           1762000000,
	} {
		if got, err := parseAt(v, now); err != nil || got != want {
			t.Errorf("--at %q = %d, %v; want %d", v, got, err, want)
		}
	}
}
