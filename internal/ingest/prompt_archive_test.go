package ingest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/exclude"
	"github.com/Coder8124/logos/internal/ingest"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// promptLine is one Claude Code content line. cwd is the project's directory,
// which is where the reader gets the project from.
func promptLine(role, cwd, text string) string {
	return fmt.Sprintf(`{"type":%q,"sessionId":"3F9A-prompt-session","cwd":%q,"timestamp":"2026-11-26T10:00:00Z","message":{"role":%q,"content":%q}}`+"\n", role, cwd, role, text)
}

// hostFile puts a transcript where Claude Code keeps one: a slug directory
// whose name is not the project's, because that is the case the derivation
// has to survive.
func hostFile(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "-Users-me-code-eco-game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "3f9a-prompt-session.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestAKilledHostLeavesTheArchiveCurrentToItsLastPrompt(t *testing.T) {
	v := t.TempDir()
	cwd := "/Users/me/code/eco-game"
	src := hostFile(t, promptLine("user", cwd, "first prompt"))

	a, attempted := ingest.ArchiveAtPrompt(v, src)
	if !attempted || a.Err != nil {
		t.Fatalf("first prompt: attempted=%v err=%v", attempted, a.Err)
	}
	// The host keeps writing, then dies: no SessionEnd, no sweep. Each prompt
	// that landed before the kill must already be in the vault.
	appendTo(t, src, promptLine("assistant", cwd, "an answer")+promptLine("user", cwd, "second prompt"))
	a, _ = ingest.ArchiveAtPrompt(v, src)
	if a.Err != nil || a.Unchanged {
		t.Fatalf("second prompt: %+v", a)
	}
	got, err := os.ReadFile(filepath.Join(v, filepath.FromSlash(a.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "second prompt") {
		t.Errorf("the copy stopped before the last prompt: %q", got)
	}
	if want := "sessions/eco-game/_convos/3f9a-prompt-session/transcript.jsonl"; a.Path != want {
		t.Errorf("filed at %q, want %q", a.Path, want)
	}
}

func TestThePromptCopyAndTheSessionEndCopyAreOneFile(t *testing.T) {
	v := t.TempDir()
	src := hostFile(t, promptLine("user", "/Users/me/code/eco-game", "hello"))

	a, _ := ingest.ArchiveAtPrompt(v, src)
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	// The session-end and sweep path: the full reader's Session, archived by
	// ArchiveForRecord. Both must land on the one file, however the hook's
	// own --project was spelled.
	s, err := transcript.ReadFile("claude-code", src)
	if err != nil {
		t.Fatal(err)
	}
	receipt, kept := ingest.ArchiveForRecord(v, s)
	if !kept || !strings.Contains(receipt, "already archived") {
		t.Fatalf("session end did not find the prompt's copy: kept=%v %q", kept, receipt)
	}
	var copies []string
	filepath.Walk(filepath.Join(v, "sessions"), func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() && session.IsTranscriptFile(fi.Name()) {
			copies = append(copies, p)
		}
		return nil
	})
	if len(copies) != 1 {
		t.Fatalf("one conversation has %d copies: %v", len(copies), copies)
	}
}

func TestAPromptInAnExcludedProjectKeepsNoCopy(t *testing.T) {
	v := t.TempDir()
	dir := filepath.Join(t.TempDir(), "eco-game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exclude.AddProject(v, dir); err != nil {
		t.Fatal(err)
	}
	src := hostFile(t, promptLine("user", dir, "private plans"))

	a, attempted := ingest.ArchiveAtPrompt(v, src)
	if !attempted || a.Err == nil || a.Path != "" {
		t.Fatalf("an excluded project was archived: %+v", a)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions")); err == nil {
		t.Error("an excluded prompt left a sessions folder behind")
	}
}

func TestAPromptWithKeepingOffAttemptsNothing(t *testing.T) {
	v := t.TempDir()
	if _, err := ingest.SetKeepTranscripts(v, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	src := hostFile(t, promptLine("user", "/Users/me/code/eco-game", "hi"))
	if _, attempted := ingest.ArchiveAtPrompt(v, src); attempted {
		t.Error("the archive switch is off and a prompt still copied the transcript")
	}
}

func TestAFailedPromptRedactionReturnsAReasonAndKeepsNoCopy(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write to")
	}
	v := t.TempDir()
	src := hostFile(t, promptLine("user", "/Users/me/code/eco-game", "hi"))
	// The redacted stream is built in a temp file beside the copy; a folder
	// that cannot take one is how redaction fails here.
	dir, err := session.TranscriptDir(v, "eco-game", "3f9a-prompt-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	a, attempted := ingest.ArchiveAtPrompt(v, src)
	if !attempted || a.Err == nil || a.Path != "" || a.Why == "" {
		t.Fatalf("a failed redaction came back success-shaped: %+v", a)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("a failed redaction left %d files in the conversation folder", len(entries))
	}
}

func TestAPromptDoesNotStartAFirstCopyTooBigForTheHook(t *testing.T) {
	v := t.TempDir()
	src := hostFile(t, promptLine("user", "/Users/me/code/eco-game", "hi"))
	if err := os.Truncate(src, ingest.PromptArchiveBudget+1<<20); err != nil {
		t.Fatal(err)
	}
	a, attempted := ingest.ArchiveAtPrompt(v, src)
	if !attempted || a.Err == nil || a.Why != ingest.WhyTooBigForAHook {
		t.Fatalf("a first copy past the hook's budget was started: %+v", a)
	}
	if _, err := os.Stat(filepath.Join(v, "sessions")); err == nil {
		t.Error("the refused prompt left a sessions folder behind")
	}
}
