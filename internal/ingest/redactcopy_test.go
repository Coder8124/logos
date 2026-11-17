package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/transcript"
)

// A token shaped like a GitHub PAT, assembled so no scanner reads this file as
// holding one.
func fakeToken() string { return "ghp_" + strings.Repeat("a1B2c3", 6) }

// claudeSession is a small Claude Code transcript in which a tool printed a
// token, and the user marked a span private.
func claudeSession() string {
	return strings.Join([]string{
		`{"type":"user","sessionId":"s1","cwd":"/work/kestrel","timestamp":"2026-11-01T10:00:00Z","message":{"role":"user","content":"deploy it <private>my diary</private> now"}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/kestrel","timestamp":"2026-11-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01ABC","name":"Bash","input":{"command":"env | grep GITHUB"}}]}}`,
		`{"type":"user","sessionId":"s1","cwd":"/work/kestrel","timestamp":"2026-11-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01ABC","content":"GITHUB_TOKEN=` + fakeToken() + `\nok"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/work/kestrel","timestamp":"2026-11-01T10:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}, "\n") + "\n"
}

func candidateFor(src string) ingest.Candidate {
	return ingest.Candidate{Harness: "claude-code", SessionID: "s1", Source: src, Project: "kestrel"}
}

func TestAnArchivedTranscriptDoesNotContainTheTokenTheToolPrinted(t *testing.T) {
	v := t.TempDir()
	src := hostTranscript(t, claudeSession())

	ar := ingest.ArchiveTranscript(v, candidateFor(src))
	if ar.Err != nil {
		t.Fatalf("archive failed: %v", ar.Err)
	}
	got, err := os.ReadFile(filepath.Join(v, filepath.FromSlash(ar.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), fakeToken()) {
		t.Fatalf("the archived copy holds the token the tool printed:\n%s", got)
	}
	if strings.Contains(string(got), "my diary") {
		t.Errorf("the archived copy holds a span the user marked <private>")
	}
	if r := ar.Receipt(); !strings.Contains(r, "1 secret redacted") || strings.Contains(r, "raw") {
		t.Errorf("receipt %q does not say one secret was redacted", r)
	}
}

func TestARedactedArchiveStillParsesAsTheTranscriptItCameFrom(t *testing.T) {
	v := t.TempDir()
	src := hostTranscript(t, claudeSession())
	ar := ingest.ArchiveTranscript(v, candidateFor(src))
	if ar.Err != nil {
		t.Fatal(ar.Err)
	}
	copyPath := filepath.Join(v, filepath.FromSlash(ar.Path))

	orig, err := transcript.ReadFile("claude-code", src)
	if err != nil {
		t.Fatal(err)
	}
	red, err := transcript.ReadFile("claude-code", copyPath)
	if err != nil {
		t.Fatalf("the redacted copy no longer parses: %v", err)
	}
	if len(red.Turns) != len(orig.Turns) || red.ID != orig.ID || red.Cwd != orig.Cwd {
		t.Fatalf("copy reads as %d turns, id %q, cwd %q; the source reads as %d turns, id %q, cwd %q",
			len(red.Turns), red.ID, red.Cwd, len(orig.Turns), orig.ID, orig.Cwd)
	}
	for _, turn := range red.Turns {
		if strings.Contains(turn.Text, fakeToken()) {
			t.Errorf("a turn read from the copy still holds the token: %q", turn.Text)
		}
	}
	// Every line is still JSON, and the ids that link a result to its call are
	// as the host wrote them.
	body, _ := os.ReadFile(copyPath)
	if !strings.Contains(string(body), `"tool_use_id":"toolu_01ABC"`) {
		t.Errorf("redaction rewrote a tool_use_id:\n%s", body)
	}
}

func TestANonJSONLineIsMaskedAsTextAndKeepsTheFileReadable(t *testing.T) {
	v := t.TempDir()
	body := "not json: " + fakeToken() + "\n" + `{"type":"user","message":"x"}` + "\n"
	ar := ingest.ArchiveTranscript(v, candidateFor(hostTranscript(t, body)))
	if ar.Err != nil {
		t.Fatal(ar.Err)
	}
	got, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(ar.Path)))
	if strings.Contains(string(got), fakeToken()) || !strings.Contains(string(got), `{"type":"user","message":"x"}`) {
		t.Errorf("copy = %q", got)
	}
}

func TestAVaultNobodyAskedArchivesItsTranscripts(t *testing.T) {
	v := t.TempDir() // no auto.md, no consent marker: nobody was asked anything
	if !ingest.KeepsTranscripts(v) {
		t.Fatal("a vault nobody asked does not keep transcripts")
	}
	src := hostTranscript(t, claudeSession())
	s, err := transcript.ReadFile("claude-code", src)
	if err != nil {
		t.Fatal(err)
	}
	line := ingest.ArchiveForRecord(v, s)
	if !strings.Contains(line, "transcript archived") {
		t.Fatalf("the session-end path archived nothing: %q", line)
	}
	if !strings.Contains(line, "kept in the vault") && !strings.Contains(line, "keeps a copy") {
		t.Errorf("the first archive did not carry the one-time notice: %q", line)
	}
	if again := ingest.ArchiveForRecord(v, s); strings.Contains(again, "keeps a copy") {
		t.Errorf("the notice was shown twice: %q", again)
	}
}

func TestArchiveOffInAutoMdKeepsNothing(t *testing.T) {
	v := t.TempDir()
	if err := auto.Set(v, "archive", false); err != nil {
		t.Fatal(err)
	}
	// An old explicit opt-in in the marker must not outvote a switch set by hand.
	grantIngest(t, v)
	if err := os.WriteFile(ingest.ConsentPath(v), []byte(`{"granted":"2026-10-01T00:00:00Z","keep_transcripts":"2026-10-02T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ingest.KeepsTranscripts(v) {
		t.Fatal("archive: off in auto.md still keeps transcripts")
	}
	s, err := transcript.ReadFile("claude-code", hostTranscript(t, claudeSession()))
	if err != nil {
		t.Fatal(err)
	}
	if line := ingest.ArchiveForRecord(v, s); line != "" {
		t.Errorf("archive off, yet %q", line)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions")); !os.IsNotExist(err) {
		t.Errorf("archive off created %s (stat: %v)", "sessions", err)
	}
}

func TestAnOldKeepMarkerStillMeansOnUnderAutoOff(t *testing.T) {
	v := t.TempDir()
	grantIngest(t, v)
	if err := os.WriteFile(ingest.ConsentPath(v), []byte(`{"granted":"2026-10-01T00:00:00Z","keep_transcripts":"2026-10-02T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := auto.SetMaster(v, false); err != nil {
		t.Fatal(err)
	}
	if !ingest.KeepsTranscripts(v) {
		t.Error("a user who asked for keeping explicitly lost it to auto off")
	}
}

func TestARedactedCopyIsNotArchivedTwice(t *testing.T) {
	v := t.TempDir()
	src := hostTranscript(t, claudeSession())
	c := candidateFor(src)
	first := ingest.ArchiveTranscript(v, c)
	if first.Err != nil || first.Unchanged {
		t.Fatalf("first archive = %+v", first)
	}
	again := ingest.ArchiveTranscript(v, c)
	if again.Err != nil || !again.Unchanged {
		t.Fatalf("archiving the same redacted transcript again = %+v, want Unchanged", again)
	}

	// The host appends: the copy grows, still redacted.
	grown := claudeSession() + `{"type":"assistant","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"again ` + fakeToken() + `"}]}}` + "\n"
	if err := os.WriteFile(src, []byte(grown), 0o600); err != nil {
		t.Fatal(err)
	}
	g := ingest.ArchiveTranscript(v, c)
	if g.Err != nil || g.Unchanged || g.Replaced {
		t.Fatalf("a grown transcript = %+v", g)
	}
	got, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(g.Path)))
	if strings.Contains(string(got), fakeToken()) {
		t.Error("the grown copy holds the token")
	}
	if again := ingest.ArchiveTranscript(v, c); !again.Unchanged {
		t.Errorf("the grown copy was archived again: %+v", again)
	}
}

func TestACopyMadeMidLineIsExtendedWhenTheHostFinishesTheLine(t *testing.T) {
	v := t.TempDir()
	line := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"hello ` + fakeToken() + `"}}`
	src := hostTranscript(t, line[:40]) // the host was mid-write
	c := candidateFor(src)
	if ar := ingest.ArchiveTranscript(v, c); ar.Err != nil {
		t.Fatal(ar.Err)
	}
	if err := os.WriteFile(src, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ar := ingest.ArchiveTranscript(v, c)
	if ar.Err != nil || ar.Unchanged {
		t.Fatalf("a finished line was refused as a rewrite: %+v", ar)
	}
}

func TestARawCopyFromBeforeRedactionIsReplacedAndTheReceiptSaysSo(t *testing.T) {
	v := t.TempDir()
	body := claudeSession()
	src := hostTranscript(t, body)
	c := candidateFor(src)
	if err := auto.Set(v, "redact", false); err != nil {
		t.Fatal(err)
	}
	raw := ingest.ArchiveTranscript(v, c)
	if raw.Err != nil || !raw.Raw || !strings.HasPrefix(raw.Receipt(), "raw transcript archived") {
		t.Fatalf("redact off archive = %+v (%s)", raw, raw.Receipt())
	}
	if got, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(raw.Path))); string(got) != body {
		t.Fatal("a redact-off copy is not the host's bytes")
	}
	if err := auto.Set(v, "redact", true); err != nil {
		t.Fatal(err)
	}
	copies, problems, err := ingest.RawCopies(v)
	if err != nil || len(problems) > 0 || len(copies) != 1 {
		t.Fatalf("RawCopies = %v %v %v, want the one raw copy", copies, problems, err)
	}
	ar := ingest.ArchiveTranscript(v, c)
	if ar.Err != nil || !ar.Replaced || !strings.Contains(ar.Receipt(), "replacing the earlier raw copy") {
		t.Fatalf("archiving over a raw copy = %+v (%s)", ar, ar.Receipt())
	}
	if copies, _, _ := ingest.RawCopies(v); len(copies) != 0 {
		t.Errorf("a copy is still raw after it was replaced: %v", copies)
	}
}

func TestRedactingACopyTwiceChangesNothingTheSecondTime(t *testing.T) {
	v := t.TempDir()
	if err := auto.Set(v, "redact", false); err != nil {
		t.Fatal(err)
	}
	ar := ingest.ArchiveTranscript(v, candidateFor(hostTranscript(t, claudeSession())))
	if ar.Err != nil {
		t.Fatal(ar.Err)
	}
	st, err := ingest.RedactCopy(v, ar.Path)
	if err != nil || st.Secrets != 1 || st.Private != 1 {
		t.Fatalf("first pass = %+v, %v; want 1 secret and 1 private span", st, err)
	}
	if raw, _, _ := ingest.RawCopies(v); len(raw) != 0 {
		t.Errorf("redacting is not idempotent: %v", raw)
	}
}

func TestATranscriptThatCannotBeRedactedIsNotKeptRaw(t *testing.T) {
	v := t.TempDir()
	// A directory opens but cannot be read, so redacting fails mid-stream.
	src := t.TempDir()
	ar := ingest.ArchiveTranscript(v, candidateFor(src))
	if ar.Err == nil {
		t.Fatalf("a transcript that could not be redacted was archived: %+v", ar)
	}
	if !strings.HasPrefix(ar.Receipt(), "transcript not archived: ") || !strings.Contains(ar.Receipt(), "redact") {
		t.Errorf("receipt %q does not say redaction failed", ar.Receipt())
	}
	assertNoCopy(t, v)

	// A copy that was already there is left as it was.
	good := hostTranscript(t, claudeSession())
	first := ingest.ArchiveTranscript(v, candidateFor(good))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	before, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(first.Path)))
	if bad := ingest.ArchiveTranscript(v, candidateFor(src)); bad.Err == nil {
		t.Fatal("archived a directory")
	}
	after, _ := os.ReadFile(filepath.Join(v, filepath.FromSlash(first.Path)))
	if string(before) != string(after) {
		t.Error("a failed redaction changed the copy already in the vault")
	}
	entries, _ := os.ReadDir(filepath.Dir(filepath.Join(v, filepath.FromSlash(first.Path))))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("a failed redaction left %s behind", e.Name())
		}
	}
}

func assertNoCopy(t *testing.T, v string) {
	t.Helper()
	copies, err := ingest.ArchivedCopies(v)
	if err != nil || len(copies) != 0 {
		t.Errorf("copies in the vault = %v (%v), want none", copies, err)
	}
	filepath.WalkDir(v, func(p string, d os.DirEntry, _ error) error {
		if d != nil && strings.HasSuffix(p, ".partial") {
			t.Errorf("partial file left behind: %s", p)
		}
		return nil
	})
}
