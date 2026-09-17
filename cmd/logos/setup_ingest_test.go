package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
)

// A new vault is empty, and setup is where someone decides whether Logos is
// worth keeping. On a machine with months of Claude Code sessions already on
// disk, that decision was made against a zero that did not have to be one:
// `logos ingest` reads those transcripts, and setup — which has just detected
// the very hosts whose transcripts they are — never said the word.
func TestSetupSaysHowMuchHistoryIsAlreadyOnThisMachine(t *testing.T) {
	dir := setupInFakeHome(t)
	t.Setenv("LOGOS_VAULT", dir)
	projects := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(filepath.Join(projects, "-Users-someone-kestrel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "-Users-someone-kestrel", "a.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(transcript.LogosClaudeProjectsEnv, projects)

	out := captureStdout(t, func() { reportHistoryOnThisMachine() })

	if !strings.Contains(out, "logos ingest") {
		t.Errorf("setup never mentioned the command that reads this machine's history:\n%s", out)
	}
	if !strings.Contains(out, "claude-code") {
		t.Errorf("nothing said whose transcripts they are:\n%s", out)
	}
}

// Silence is the right answer when there is nothing to read: a line offering to
// recover history that does not exist is the same false promise as a healthy
// zero.
func TestSetupSaysNothingAboutHistoryWhenThereIsNone(t *testing.T) {
	setupInFakeHome(t)
	t.Setenv(transcript.LogosClaudeProjectsEnv, filepath.Join(t.TempDir(), "nothing"))

	out := captureStdout(t, func() { reportHistoryOnThisMachine() })

	if strings.Contains(out, "ingest") {
		t.Errorf("setup offered to read history this machine does not have:\n%s", out)
	}
}
