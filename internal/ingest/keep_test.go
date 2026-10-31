package ingest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/transcript"
)

// hostTranscript writes a stand-in for a host's JSONL transcript outside the
// vault, where a host keeps it.
func hostTranscript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "4b1e9c2a-host.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func grantIngest(t *testing.T, vaultDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(vaultDir, ".logos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ingest.ConsentPath(vaultDir), []byte(`{"granted":"2026-10-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAHarvestArchivesTheTranscriptIntoTheConvoFolder(t *testing.T) {
	v := t.TempDir()
	src := hostTranscript(t, `{"type":"user","message":"one"}`+"\n")
	c := ingest.Candidate{Harness: "claude-code", SessionID: "4B1E9C2A-host", Source: src, Project: "kestrel"}

	ar := ingest.ArchiveTranscript(v, c)
	if ar.Err != nil {
		t.Fatalf("archive failed: %v", ar.Err)
	}
	want := "sessions/kestrel/_convos/4b1e9c2a-host/transcript.jsonl"
	if ar.Path != want {
		t.Fatalf("archived at %q, want %q", ar.Path, want)
	}
	dst := filepath.Join(v, filepath.FromSlash(want))
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("archived copy is %v; a raw transcript must be 0600", fi.Mode().Perm())
	}

	// Unchanged is read off the file, so a second harvest of the same bytes
	// says so instead of announcing a fresh copy.
	if again := ingest.ArchiveTranscript(v, c); !again.Unchanged || again.Err != nil {
		t.Errorf("re-archiving identical bytes = %+v, want Unchanged", again)
	}

	// The host appends; the next harvest replaces the copy in place.
	grown := `{"type":"user","message":"one"}` + "\n" + `{"type":"assistant","message":"two"}` + "\n"
	if err := os.WriteFile(src, []byte(grown), 0o600); err != nil {
		t.Fatal(err)
	}
	ar = ingest.ArchiveTranscript(v, c)
	if ar.Err != nil || ar.Unchanged || ar.Path != want {
		t.Fatalf("re-archiving a grown transcript = %+v", ar)
	}
	if got, _ := os.ReadFile(dst); string(got) != grown {
		t.Errorf("archived copy is %q, want the grown transcript", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("a finished archive left %s behind", e.Name())
		}
	}
}

// A conversation whose checkpoints are filed under a worktree scope has its
// transcript filed beside them, not in a second folder under the project the
// transcript's cwd names.
func TestAnArchivedTranscriptJoinsItsConversationsExistingWorktreeFolder(t *testing.T) {
	v := t.TempDir()
	convo := filepath.Join(v, "sessions", "kestrel", "kestrel-wt", "_convos", "4b1e9c2a-host")
	if err := os.MkdirAll(convo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convo, "20261020-101010-claude-code.md"), []byte("---\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := ingest.Candidate{Harness: "claude-code", SessionID: "4b1e9c2a-host", Source: hostTranscript(t, "x\n"), Project: "kestrel-wt"}
	ar := ingest.ArchiveTranscript(v, c)
	if want := "sessions/kestrel/kestrel-wt/_convos/4b1e9c2a-host/transcript.jsonl"; ar.Err != nil || ar.Path != want {
		t.Fatalf("archived = %+v, want path %q", ar, want)
	}
}

func TestAMissingHostTranscriptIsReportedNotSilentlySkipped(t *testing.T) {
	v := t.TempDir()
	gone := filepath.Join(t.TempDir(), "pruned.jsonl")
	ar := ingest.ArchiveTranscript(v, ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: gone, Project: "kestrel"})
	if ar.Err == nil {
		t.Fatalf("archiving a deleted transcript succeeded: %+v", ar)
	}
	if !strings.Contains(ar.Err.Error(), gone) {
		t.Errorf("the error %q does not name the file that is missing", ar.Err)
	}
	if r := ar.Receipt(); !strings.HasPrefix(r, "raw transcript not archived: ") {
		t.Errorf("receipt %q does not say the transcript was not archived", r)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions")); !os.IsNotExist(err) {
		t.Errorf("a failed archive created folders in the vault (stat: %v)", err)
	}
}

// Every transcript that cannot be copied per conversation is refused with a
// reason, never guessed into a shared folder.
func TestATranscriptWithNoFileOfItsOwnOrNoHomeIsRefusedWithAReason(t *testing.T) {
	v := t.TempDir()
	src := hostTranscript(t, "x\n")
	for name, c := range map[string]ingest.Candidate{
		"cursor":     {Harness: "cursor", SessionID: "c1", Source: src + "#chat-1", Project: "kestrel"},
		"txcript":    {Harness: "aider", SessionID: "t1", Source: transcript.TxcriptPrefix + "aider/t1", Project: "kestrel"},
		"no id":      {Harness: "claude-code", Source: src, Project: "kestrel"},
		"no project": {Harness: "claude-code", SessionID: "s1", Source: src},
	} {
		if ar := ingest.ArchiveTranscript(v, c); ar.Err == nil {
			t.Errorf("%s: archived to %q, want a refusal", name, ar.Path)
		}
	}
}

func TestReceiptNamesTheArchivedTranscriptSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1 KB", 41983: "41 KB", 3 << 20: "3.0 MB"} {
		if got := ingest.Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
	v := t.TempDir()
	src := hostTranscript(t, strings.Repeat("y", 41983))
	r := ingest.ArchiveTranscript(v, ingest.Candidate{Harness: "codex", SessionID: "s1", Source: src, Project: "kestrel"}).Receipt()
	if r != "raw transcript archived (41 KB)" {
		t.Errorf("receipt = %q", r)
	}
}

// Keeping is off for every user who never asked for it — including one who
// granted ingest before the setting existed — and turning it on keeps the
// grant it narrows.
func TestKeepingTranscriptsIsOffUntilTurnedOnAndKeepsTheGrant(t *testing.T) {
	v := t.TempDir()
	if ingest.KeepsTranscripts(v) {
		t.Fatal("keeping is on in a vault with no marker")
	}
	if _, err := ingest.SetKeepTranscripts(v, true, time.Now()); err == nil {
		t.Fatal("keeping was turned on without the ingest grant")
	}
	grantIngest(t, v)
	if ingest.KeepsTranscripts(v) {
		t.Fatal("a grant from before the setting existed turned keeping on")
	}
	if changed, err := ingest.SetKeepTranscripts(v, true, time.Now()); err != nil || !changed {
		t.Fatalf("turning keeping on = %v, %v", changed, err)
	}
	if changed, _ := ingest.SetKeepTranscripts(v, true, time.Now()); changed {
		t.Error("turning keeping on twice reported a change, so the warning would repeat")
	}
	if !ingest.KeepsTranscripts(v) || !ingest.HasConsent(v) {
		t.Fatalf("after turning keeping on: keeps=%v, consent=%v", ingest.KeepsTranscripts(v), ingest.HasConsent(v))
	}
	b, _ := os.ReadFile(ingest.ConsentPath(v))
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil || m["granted"] != "2026-10-01T00:00:00Z" {
		t.Errorf("the marker lost or changed its grant: %s", b)
	}
	if changed, err := ingest.SetKeepTranscripts(v, false, time.Now()); err != nil || !changed || ingest.KeepsTranscripts(v) {
		t.Errorf("turning keeping off = %v, %v; keeps=%v", changed, err, ingest.KeepsTranscripts(v))
	}
}

// A host that writes markdown would otherwise have its raw transcript read as a
// note by every walker of sessions/ that keys on ".md".
func TestAMarkdownTranscriptIsNeverArchivedUnderANoteShapedName(t *testing.T) {
	v := t.TempDir()
	src := filepath.Join(t.TempDir(), "chat.md")
	if err := os.WriteFile(src, []byte("# chat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ar := ingest.ArchiveTranscript(v, ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: src, Project: "kestrel"})
	if ar.Err != nil || strings.HasSuffix(ar.Path, ".md") || !strings.HasSuffix(ar.Path, "/transcript.md.txt") {
		t.Errorf("archived = %+v, want transcript.md.txt", ar)
	}
}

// The copy is the only record of what a session said before its host
// compacted it: a source that no longer extends the copy must not replace it.
func TestAnArchivedCopyIsNeverReplacedByAShorterOrRewrittenTranscript(t *testing.T) {
	for name, rewrite := range map[string]string{
		"shorter":   `{"type":"user","message":"one"}` + "\n",
		"rewritten": `{"type":"user","message":"ONE"}` + "\n" + `{"type":"assistant","message":"two"}` + "\n" + "extra\n",
	} {
		t.Run(name, func(t *testing.T) {
			v := t.TempDir()
			held := `{"type":"user","message":"one"}` + "\n" + `{"type":"assistant","message":"two"}` + "\n"
			src := hostTranscript(t, held)
			c := ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: src, Project: "kestrel"}
			first := ingest.ArchiveTranscript(v, c)
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			if err := os.WriteFile(src, []byte(rewrite), 0o600); err != nil {
				t.Fatal(err)
			}
			ar := ingest.ArchiveTranscript(v, c)
			if ar.Err == nil || !strings.Contains(ar.Why, "rewrote") {
				t.Errorf("a %s source replaced the copy: %+v", name, ar)
			}
			if got, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(first.Path))); string(got) != held {
				t.Errorf("the copy changed to %q", got)
			}
		})
	}
}

// A session the user rejected in review is one they said no to; keeping its
// raw text after that holds on to the thing they declined.
func TestARejectedSessionsTranscriptIsNotArchived(t *testing.T) {
	v := t.TempDir()
	c := ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: hostTranscript(t, "x\n"),
		Project: "kestrel", Hash: "h1", Status: ingest.StatusPending}
	res, err := ingest.Put(v, c)
	if err != nil || !res.Written {
		t.Fatalf("put = %+v, %v", res, err)
	}
	if err := ingest.Reject(filepath.Join(v, filepath.FromSlash(res.Path)), c); err != nil {
		t.Fatal(err)
	}
	ar := ingest.ArchiveTranscript(v, c)
	if ar.Err == nil || !strings.Contains(ar.Why, "rejected") {
		t.Fatalf("a rejected session was archived: %+v", ar)
	}
	matches, _ := filepath.Glob(filepath.Join(v, "sessions", "*", "_convos", "*", "transcript.*"))
	if len(matches) != 0 {
		t.Errorf("a rejected session left %v in the vault", matches)
	}
}

// Two harvests of one conversation at once each write their own temp file, so
// neither renames the other's half-written bytes into place.
func TestConcurrentArchivesOfOneConversationLeaveOneWholeCopy(t *testing.T) {
	v := t.TempDir()
	body := strings.Repeat(`{"type":"user","message":"a fairly long line"}`+"\n", 20000)
	c := ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: hostTranscript(t, body), Project: "kestrel"}
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { errs <- ingest.ArchiveTranscript(v, c).Err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent archive: %v", err)
		}
	}
	dir := filepath.Join(v, "sessions", "kestrel", "_convos", "s1")
	if got, _ := os.ReadFile(filepath.Join(dir, "transcript.jsonl")); string(got) != body {
		t.Errorf("the copy is %d bytes, want the whole %d", len(got), len(body))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("concurrent archives left %d files, want only the copy", len(entries))
	}
}

// A grouping key is a reason, not a reason plus a path.
func TestAMissingTranscriptsReasonDoesNotNameTheFile(t *testing.T) {
	v := t.TempDir()
	a := ingest.ArchiveTranscript(v, ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: "/gone/a.jsonl", Project: "kestrel"})
	b := ingest.ArchiveTranscript(v, ingest.Candidate{Harness: "claude-code", SessionID: "s2", Source: "/gone/b.jsonl", Project: "kestrel"})
	if a.Why == "" || a.Why != b.Why {
		t.Errorf("two missing files group as %q and %q, want one shared reason", a.Why, b.Why)
	}
}
