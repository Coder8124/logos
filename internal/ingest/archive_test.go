package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/ingest"
)

// putHarvest writes one harvest candidate citing a transcript this test owns,
// and returns the transcript's path.
func putHarvest(t *testing.T, vault, id, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, name)
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Put(vault, ingest.Candidate{
		Harness:   "codex",
		SessionID: id,
		Source:    src,
		Hash:      "hash-" + id,
		Project:   "gadgets",
		Tier:      ingest.TierHarvest,
	}); err != nil {
		t.Fatal(err)
	}
	return src
}

// sourceOf returns the source recorded on the one candidate whose session id
// matches, so a test can assert what the queue will read after the archive.
func sourceOf(t *testing.T, vault, id string) string {
	t.Helper()
	all, skipped := ingest.All(vault)
	if len(skipped) > 0 {
		t.Fatalf("candidate files unreadable after archiving: %v", skipped)
	}
	for _, c := range all {
		if c.SessionID == id {
			return c.Source
		}
	}
	t.Fatalf("no candidate %s in the queue", id)
	return ""
}

// #115: distilling re-reads the source transcript, which logos deliberately
// does not own — so the window to upgrade a harvest closes on a date somebody
// else picks (a corp wipe, a log rotation). Archiving is the user's way to keep
// that window open on their own storage, and it is worth nothing unless the
// candidate is repointed at the copy: an archive the queue still cannot find is
// a backup of a file nobody reads.
func TestArchivingCopiesTheTranscriptsAndPointsTheCandidatesAtTheCopies(t *testing.T) {
	vault := t.TempDir()
	dest := filepath.Join(t.TempDir(), "keep")
	src := putHarvest(t, vault, "sess-one", "rollout.jsonl", "{\"turn\":1}\n")

	res, problems, err := ingest.Archive(vault, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("archiving a readable transcript reported problems: %v", problems)
	}
	if res.Copied != 1 {
		t.Errorf("copied %d transcripts, want 1", res.Copied)
	}

	moved := sourceOf(t, vault, "sess-one")
	if moved == src {
		t.Fatal("the candidate still cites the transcript logos does not own")
	}
	if !strings.HasPrefix(moved, dest) {
		t.Errorf("the candidate was repointed outside the archive: %q", moved)
	}

	// The whole point: the candidate survives losing the original.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(moved)
	if err != nil {
		t.Fatalf("the archived transcript is not readable after the original went: %v", err)
	}
	if string(body) != "{\"turn\":1}\n" {
		t.Errorf("the archived transcript does not match the original: %q", body)
	}
}

// Cursor addresses a session as <state.vscdb>#<chat id>, and every session in
// one workspace shares that database. Dropping the fragment archives the file
// and loses which chat the candidate was, and copying per candidate would
// duplicate the whole database once per chat.
func TestArchivingACursorSourceKeepsTheChatIdAndCopiesTheDatabaseOnce(t *testing.T) {
	vault := t.TempDir()
	dest := filepath.Join(t.TempDir(), "keep")

	dir := t.TempDir()
	db := filepath.Join(dir, "state.vscdb")
	if err := os.WriteFile(db, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, chat := range []string{"chat-a", "chat-b"} {
		if _, err := ingest.Put(vault, ingest.Candidate{
			Harness:   "cursor",
			SessionID: chat,
			Source:    db + "#" + chat,
			Hash:      "hash-" + chat,
			Tier:      ingest.TierHarvest,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, problems, err := ingest.Archive(vault, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("archiving reported problems: %v", problems)
	}
	if res.Copied != 1 {
		t.Errorf("the shared database was copied %d times, want once", res.Copied)
	}
	if res.Repointed != 2 {
		t.Errorf("repointed %d candidates, want both", res.Repointed)
	}

	for _, chat := range []string{"chat-a", "chat-b"} {
		got := sourceOf(t, vault, chat)
		if !strings.HasSuffix(got, "#"+chat) {
			t.Errorf("%s lost the chat id that addresses it inside the database: %q", chat, got)
		}
		if _, err := os.Stat(strings.TrimSuffix(got, "#"+chat)); err != nil {
			t.Errorf("%s points at no archived database: %v", chat, err)
		}
	}
}

// Invariant 4: the transcript that is already gone is the reason the user is
// running this, and it is the one thing they must be told about by name. A
// silent count of "3 archived" out of 5 is the failure shaped like a success.
func TestArchivingNamesTheTranscriptsItCouldNotCopyInsteadOfSkippingThem(t *testing.T) {
	vault := t.TempDir()
	dest := filepath.Join(t.TempDir(), "keep")
	putHarvest(t, vault, "sess-live", "live.jsonl", "here\n")
	gone := putHarvest(t, vault, "sess-gone", "gone.jsonl", "not for long\n")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	res, problems, err := ingest.Archive(vault, dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Copied != 1 {
		t.Errorf("copied %d, want only the surviving transcript", res.Copied)
	}
	if res.Missing != 1 {
		t.Errorf("missing counted %d, want 1", res.Missing)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "gone.jsonl") {
		t.Errorf("the transcript that could not be copied was not named: %v", problems)
	}
	// The one that could not be copied keeps citing where it was: that path is
	// the provenance line a reviewer reads, and blanking it loses the record of
	// what the candidate came from.
	if got := sourceOf(t, vault, "sess-gone"); got != gone {
		t.Errorf("a failed copy rewrote the candidate anyway: %q", got)
	}
}

// Running it twice is how a user who archives before every corp deadline uses
// it. The second run has nothing left to do, and must not copy the archive into
// itself or rewrite paths that are already right.
func TestArchivingTwiceCopiesNothingTheSecondTime(t *testing.T) {
	vault := t.TempDir()
	dest := filepath.Join(t.TempDir(), "keep")
	putHarvest(t, vault, "sess-one", "rollout.jsonl", "{\"turn\":1}\n")

	if _, _, err := ingest.Archive(vault, dest); err != nil {
		t.Fatal(err)
	}
	first := sourceOf(t, vault, "sess-one")

	res, problems, err := ingest.Archive(vault, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("the second run reported problems: %v", problems)
	}
	if res.Copied != 0 {
		t.Errorf("the second run copied %d transcripts that were already archived", res.Copied)
	}
	if res.Already != 1 {
		t.Errorf("already-archived counted %d, want 1", res.Already)
	}
	if got := sourceOf(t, vault, "sess-one"); got != first {
		t.Errorf("the second run moved the candidate again: %q then %q", first, got)
	}
}
