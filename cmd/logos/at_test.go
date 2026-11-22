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
		// Through the flag check the binary runs first: the unit call alone
		// passed while `logos note` refused --at as an unknown flag.
		noteArgs := []string{"harrier", "--at", "2026-03-02T09:30", "tried the 3.3V rail, it browned out"}
		if err := checkCommandFlags("note", noteArgs); err != nil {
			t.Fatalf("the note's flag check refused --at: %v", err)
		}
		if err := runNote(noteArgs); err != nil {
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
	// 20261103 and 2026 are a date and a year typed without dashes; read as
	// unix seconds they filed a record in 1970 and said nothing.
	for _, v := range []string{"2027-01-01", "tuesday", "", "20261103", "2026", "@0", "@-5", "@x"} {
		if _, err := parseAt(v, now); err == nil {
			t.Errorf("--at %q was accepted", v)
		}
	}
	for v, want := range map[string]int64{
		"2026-11-03":           time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local).Unix(),
		"2026-11-03T14:05":     time.Date(2026, 11, 3, 14, 5, 0, 0, time.Local).Unix(),
		"2026-11-03T14:05:00Z": time.Date(2026, 11, 3, 14, 5, 0, 0, time.UTC).Unix(),
		"@1762000000":          1762000000,
	} {
		if got, err := parseAt(v, now); err != nil || got != want {
			t.Errorf("--at %q = %d, %v; want %d", v, got, err, want)
		}
	}
}

// A note typed unquoted that mentions the flag keeps its words: --at is a
// flag only before the text.
func TestANoteThatMentionsAtInItsTextIsRefusedRatherThanLosingWords(t *testing.T) {
	t.Setenv("LOGOS_VAULT", t.TempDir())
	t.Setenv("LOGOS_EMBED", "off")
	err := runNote([]string{"brain", "fixed", "the", "--at", "parser"})
	if err == nil || !strings.Contains(err.Error(), "before the note's text") {
		t.Fatalf("got %v, want a refusal that says where --at goes", err)
	}
}

// A checkpoint dated last week describes last week. Notes written after that
// date, the git state of today, and a newer checkpoint are all things it
// never saw, and each was being folded or linked into it.
func TestABackdatedCheckpointTakesOnlyWhatCameBeforeIt(t *testing.T) {
	vaultDir := t.TempDir()
	t.Setenv("LOGOS_VAULT", vaultDir)
	t.Setenv("LOGOS_EMBED", "off")
	t.Setenv("LOGOS_PROJECT", "kestrel")
	t.Setenv("LOGOS_AGENT", "claude")

	said := captureStdout(t, func() {
		for _, step := range [][]string{
			{"--task", "first", "--state", "the oldest checkpoint", "--at", "2026-03-01T10:00"},
			{"--task", "newest", "--state", "the newest checkpoint", "--at", "2026-03-10T10:00"},
		} {
			if err := runCheckpoint(step); err != nil {
				t.Fatalf("checkpoint %v: %v", step, err)
			}
		}
		if err := runNote([]string{"kestrel", "--at", "2026-03-04T09:00", "before the backdated checkpoint"}); err != nil {
			t.Fatal(err)
		}
		if err := runNote([]string{"kestrel", "--at", "2026-03-08T09:00", "after the backdated checkpoint"}); err != nil {
			t.Fatal(err)
		}
		if err := runCheckpoint([]string{"--task", "middle", "--state", "filed in between", "--at", "2026-03-05T10:00"}); err != nil {
			t.Fatal(err)
		}
	})

	all, err := session.History(vaultPath(), "kestrel", 0)
	if err != nil {
		t.Fatal(err)
	}
	var first, middle *session.Checkpoint
	for i := range all {
		switch all[i].Task {
		case "first":
			first = &all[i]
		case "middle":
			middle = &all[i]
		}
	}
	if first == nil || middle == nil {
		t.Fatalf("missing checkpoints in %d", len(all))
	}
	if !strings.Contains(middle.State, "before the backdated checkpoint") {
		t.Errorf("the earlier note was not taken:\n%s", middle.State)
	}
	if strings.Contains(middle.State, "after the backdated checkpoint") {
		t.Errorf("a note written after the checkpoint's date was folded into it:\n%s", middle.State)
	}
	if !middle.Git.Empty() {
		t.Errorf("a backdated checkpoint recorded today's repository: %+v", middle.Git)
	}
	raw, err := os.ReadFile(filepath.Join(vaultDir, filepath.FromSlash(middle.Slug)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pred: follows, obj: \"[["+first.Session+"]]\"") {
		t.Errorf("the backdated checkpoint should follow %s, the one before its date:\n%s", first.Session, raw)
	}

	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	left, err := session.Uncommitted(ix.DB, "kestrel")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Text != "after the backdated checkpoint" {
		t.Errorf("want only the later note still uncommitted, got %+v", left)
	}

	if strings.Count(said, "`logos resume` picks it up now") != 2 {
		t.Errorf("only the two checkpoints that became the latest may say resume picks them up:\n%s", said)
	}
	if !strings.Contains(said, "filed in the history before") {
		t.Errorf("the backdated checkpoint did not say it is not the latest:\n%s", said)
	}
}
