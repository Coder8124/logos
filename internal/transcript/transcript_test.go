package transcript_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/brain/internal/transcript"
)

// The fixture trees live under testdata/transcripts/<harness>/. Pointing the
// BRAIN_* overrides at them keeps these tests off the developer's real
// ~/.claude and ~/.codex directories.
func claudeRoot(t *testing.T) string {
	t.Helper()
	return abs(t, "testdata/transcripts/claude-code")
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAClaudeCodeSessionParsesIntoOrderedTurns(t *testing.T) {
	t.Setenv(transcript.BrainClaudeProjectsEnv, claudeRoot(t))

	paths, err := transcript.Sessions("claude-code")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	var path string
	for _, p := range paths {
		if strings.Contains(p, "11111111-2222-3333-4444-555555555555") {
			path = p
		}
	}
	if path == "" {
		t.Fatalf("fixture session not discovered in %v", paths)
	}

	s, err := transcript.ReadFile("claude-code", path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if s.Project != "widgets" {
		t.Errorf("Project = %q, want widgets", s.Project)
	}
	if s.ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", s.Skipped)
	}

	gotRoles := roles(s)
	wantRoles := []string{"user", "assistant", "tool", "assistant", "tool", "assistant"}
	if strings.Join(gotRoles, ",") != strings.Join(wantRoles, ",") {
		t.Fatalf("roles = %v, want %v", gotRoles, wantRoles)
	}
	if s.Turns[2].Status != "error" {
		t.Errorf("first tool turn status = %q, want error", s.Turns[2].Status)
	}
	if s.Turns[4].Status != "ok" {
		t.Errorf("second tool turn status = %q, want ok", s.Turns[4].Status)
	}
	if !strings.Contains(s.Turns[2].Text, "undefined: Frob") {
		t.Errorf("error tool turn text = %q", s.Turns[2].Text)
	}
	if s.Turns[2].Tool != "Bash" {
		t.Errorf("tool name = %q, want Bash", s.Turns[2].Tool)
	}
}

func TestACodexSessionParsesIntoOrderedTurns(t *testing.T) {
	t.Setenv(transcript.BrainCodexSessionsEnv, abs(t, "testdata/transcripts/codex"))

	paths, err := transcript.Sessions("codex")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("discovered %d sessions, want 1: %v", len(paths), paths)
	}

	s, err := transcript.ReadFile("codex", paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if s.Project != "gadgets" {
		t.Errorf("Project = %q, want gadgets", s.Project)
	}
	if s.ID != "01a05124-f309-7cd3-86e6-d0d303880456" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", s.Skipped)
	}

	got := roles(s)
	want := []string{"user", "assistant", "tool", "tool", "assistant"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if s.Turns[2].Status != "error" {
		t.Errorf("custom_tool_call_output status = %q, want error", s.Turns[2].Status)
	}
	if s.Turns[3].Status != "ok" {
		t.Errorf("function_call_output status = %q, want ok", s.Turns[3].Status)
	}
	for _, tn := range s.Turns {
		if strings.Contains(tn.Text, "must not be cited") {
			t.Errorf("reasoning summary leaked into a turn: %q", tn.Text)
		}
	}
}

func TestAMalformedLineIsSkippedAndCountedRatherThanFailingTheWholeSession(t *testing.T) {
	root := abs(t, "testdata/transcripts/claude-code-malformed")
	t.Setenv(transcript.BrainClaudeProjectsEnv, root)

	paths, err := transcript.Sessions("claude-code")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("want 1 session, got %v", paths)
	}

	s, err := transcript.ReadFile("claude-code", paths[0])
	if err != nil {
		t.Fatalf("a session with one bad line must still parse: %v", err)
	}
	if s.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", s.Skipped)
	}
	got := roles(s)
	want := []string{"user", "assistant"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v (lines 1 and 3 survive, line 2 is skipped)", got, want)
	}
}

func TestAHarnessWithNoReaderIsReportedWithTheReasonItWasSkipped(t *testing.T) {
	_, err := transcript.Sessions("emacs-gptel")
	if err == nil {
		t.Fatal("want an error for an unknown harness")
	}
	var skip *transcript.SkipReason
	if !errors.As(err, &skip) {
		t.Fatalf("want *SkipReason, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "emacs-gptel") || skip.Why == "" {
		t.Errorf("skip reason does not name the harness and the fix: %v", err)
	}

	// A known txcript-backed harness with the binary absent says so too, rather
	// than vanishing from Available().
	var cursor transcript.Availability
	for _, a := range transcript.Available() {
		if a.Harness == "cursor" {
			cursor = a
		}
	}
	if cursor.Harness == "" {
		t.Fatal("cursor missing from Available()")
	}
	if _, err := execLookTxcript(); err != nil {
		if cursor.Found {
			t.Errorf("cursor reported found with no txcript on PATH")
		}
		if !strings.Contains(cursor.Reason, "txcript") {
			t.Errorf("cursor skip reason = %q, want it to mention txcript", cursor.Reason)
		}
	}
}

func TestSimpleInterchangeJSONOnStdinNeedsNoAdapter(t *testing.T) {
	const doc = `{
	  "harness": "some-new-agent",
	  "project": "acme",
	  "messages": [
	    {"role": "user", "content": "add a healthcheck endpoint"},
	    {"role": "assistant", "content": "Added /healthz."},
	    {"role": "tool", "tool": "bash", "status": "ok", "content": "PASS"}
	  ]
	}`

	s, err := transcript.ReadInterchange(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadInterchange: %v", err)
	}
	if s.Harness != "some-new-agent" || s.Project != "acme" {
		t.Errorf("Harness/Project = %q/%q", s.Harness, s.Project)
	}
	if s.Path != "(stdin)" {
		t.Errorf("Path = %q, want (stdin)", s.Path)
	}
	got := roles(s)
	want := []string{"user", "assistant", "tool"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if s.Turns[2].Status != "ok" {
		t.Errorf("tool status = %q", s.Turns[2].Status)
	}
	if s.Hash == "" {
		t.Error("interchange session has no content hash")
	}
}

func TestIngestNeverWritesToTheSourceTranscript(t *testing.T) {
	// Copy the fixtures into a temp tree so we can compare mtimes without
	// touching version-controlled files.
	dir := t.TempDir()
	claudeSrc := abs(t, "testdata/transcripts/claude-code")
	claudeDst := filepath.Join(dir, "claude-code")
	copyTree(t, claudeSrc, claudeDst)
	codexSrc := abs(t, "testdata/transcripts/codex")
	codexDst := filepath.Join(dir, "codex")
	copyTree(t, codexSrc, codexDst)

	before := snapshotMtimes(t, dir)

	t.Setenv(transcript.BrainClaudeProjectsEnv, claudeDst)
	t.Setenv(transcript.BrainCodexSessionsEnv, codexDst)

	for _, h := range []string{"claude-code", "codex"} {
		paths, err := transcript.Sessions(h)
		if err != nil {
			t.Fatalf("Sessions(%s): %v", h, err)
		}
		for _, p := range paths {
			if _, err := transcript.ReadFile(h, p); err != nil {
				t.Fatalf("ReadFile(%s, %s): %v", h, p, err)
			}
		}
	}

	after := snapshotMtimes(t, dir)
	for path, mt := range before {
		if !after[path].Equal(mt) {
			t.Errorf("source transcript mtime changed: %s (%v -> %v)", path, mt, after[path])
		}
	}
	if len(after) != len(before) {
		t.Errorf("file count changed: %d -> %d (a reader created or deleted a file)", len(before), len(after))
	}
}

// --- helpers ---

func roles(s *transcript.Session) []string {
	out := make([]string, len(s.Turns))
	for i, tn := range s.Turns {
		out[i] = tn.Role
	}
	return out
}

func execLookTxcript() (string, error) {
	return exec.LookPath("txcript")
}

func snapshotMtimes(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = fi.ModTime()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Claude Code names a project directory by replacing every separator in the
// cwd with "-", which is not reversible: splitting on "-" and keeping the last
// segment turns eco-game into "game" and eng-lish into "lish". The damage is
// not cosmetic — Codex records a real cwd and attributes the same repository
// correctly, so one project split into two vault directories depending on
// which harness a session came from, and `brain resume eng-lish` saw half its
// history. The recorded cwd is authoritative; the slug is only the fallback.
func TestAHyphenatedProjectKeepsItsWholeNameNotTheTailAfterTheLastDash(t *testing.T) {
	t.Setenv(transcript.BrainClaudeProjectsEnv, claudeRoot(t))

	path := filepath.Join(claudeRoot(t), "-Users-alice-code-eco-game",
		"99999999-8888-7777-6666-555555555555.jsonl")
	s, err := transcript.ReadFile("claude-code", path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if s.Project != "eco-game" {
		t.Errorf("Project = %q, want eco-game (the cwd's basename, not the slug tail)", s.Project)
	}
}
