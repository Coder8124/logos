package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/brain/internal/transcript"
)

// scratchIngest points the transcript readers at the checked-in fixtures and
// gives back a fresh vault. Every ingest CLI test runs against this, never a
// real ~/.claude/projects.
func scratchIngest(t *testing.T) (vaultDir string) {
	t.Helper()
	vaultDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_VAULT", vaultDir)
	t.Setenv(transcript.BrainClaudeProjectsEnv, absFixture(t, "claude-code"))
	t.Setenv(transcript.BrainCodexSessionsEnv, absFixture(t, "codex"))
	return vaultDir
}

func absFixture(t *testing.T, harness string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "internal", "transcript", "testdata", "transcripts", harness))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The whole point of `brain ingest` is that it says what it did. A run that
// read three transcripts and queued two must print both numbers (invariant 3),
// not just leave new files in the vault.
func TestIngestPrintsWhatItReadAndWhatItQueued(t *testing.T) {
	scratchIngest(t)

	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})

	if !strings.Contains(out, "transcript(s) read") {
		t.Errorf("output does not report what was read:\n%s", out)
	}
	if !strings.Contains(out, "2 queued") {
		t.Errorf("output does not report two queued candidates:\n%s", out)
	}
	if !strings.Contains(out, "review them:  brain ingest review") {
		t.Errorf("output does not point at the review step:\n%s", out)
	}
}

// A transcript that cannot be read is named on stdout and left out of the
// queued count — never dropped silently (invariant 4).
func TestAnUnreadableTranscriptIsNamedInTheOutputNotSwallowed(t *testing.T) {
	vaultDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultDir, ".brain"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_VAULT", vaultDir)

	// A claude-code projects tree with one good session and one file we cannot
	// open.
	root := filepath.Join(t.TempDir(), "projects", "-Users-alice-code-widgets")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(filepath.Join(absFixture(t, "claude-code"), "-Users-alice-code-widgets", "11111111-2222-3333-4444-555555555555.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "11111111-2222-3333-4444-555555555555.jsonl"), good, 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "99999999-0000-0000-0000-000000000000.jsonl")
	if err := os.WriteFile(bad, []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o644) })
	t.Setenv(transcript.BrainClaudeProjectsEnv, filepath.Dir(root))
	t.Setenv(transcript.BrainCodexSessionsEnv, filepath.Join(t.TempDir(), "no-codex"))

	out := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})

	if !strings.Contains(out, "skipped") || !strings.Contains(out, "99999999-0000-0000-0000-000000000000.jsonl") {
		t.Errorf("the unreadable transcript was not named in the output:\n%s", out)
	}
	if !strings.Contains(out, "1 unreadable") {
		t.Errorf("the unreadable transcript was not counted:\n%s", out)
	}
}

// --dry-run is the safe preview: it reads transcripts and prints what it would
// queue, but the vault is untouched — no ingest/ directory, no consent marker.
func TestDryRunWritesNothingToTheVault(t *testing.T) {
	vaultDir := scratchIngest(t)

	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--dry-run"}); err != nil {
			t.Fatalf("dry run: %v", err)
		}
	})

	if !strings.Contains(out, "would ingest") || !strings.Contains(out, "nothing written") {
		t.Errorf("dry run output is wrong:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "ingest")); !os.IsNotExist(err) {
		t.Errorf("dry run created the ingest directory")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, ".brain", "ingest-consent.json")); !os.IsNotExist(err) {
		t.Errorf("dry run recorded consent it did not need")
	}
}

// Without a consent grant and without --yes, a real ingest refuses rather than
// reading transcripts anyway. (go test's stdin is not a terminal, so the
// prompt reads EOF and the answer is "no".)
func TestIngestRefusesToRunWithoutConsent(t *testing.T) {
	scratchIngest(t)

	err := runIngest([]string{"--all-projects"})
	if err == nil {
		t.Fatal("ingest ran without consent")
	}
	if !strings.Contains(err.Error(), "consent") {
		t.Errorf("error does not explain the consent requirement: %v", err)
	}
}

// After ingesting, `brain resume` for a project with nothing else recorded still
// tells the reader the queue is waiting — otherwise the candidates look lost.
func TestResumeMentionsPendingCandidates(t *testing.T) {
	scratchIngest(t)
	if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runResume([]string{"widgets"}); err != nil {
			t.Fatalf("resume: %v", err)
		}
	})

	if !strings.Contains(out, "pending review") || !strings.Contains(out, "brain ingest review") {
		t.Errorf("resume did not mention the pending ingest queue:\n%s", out)
	}
}
