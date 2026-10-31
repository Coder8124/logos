package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

const fixtureSession = "11111111-2222-3333-4444-555555555555"

// ownedHostCopy points the Claude Code reader at a copy of one fixture
// transcript the test may change or delete, and returns that copy's path.
func ownedHostCopy(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(absFixture(t, "claude-code"), "-Users-alice-code-widgets", fixtureSession+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "-Users-alice-code-widgets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, fixtureSession+".jsonl")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	return p
}

func TestTranscriptCommandReadsTheArchivedCopyNotTheHostPath(t *testing.T) {
	scratchIngest(t)
	host := ownedHostCopy(t)
	want, _ := os.ReadFile(host)
	captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes", "--keep-transcripts"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})

	// The host rewrites its file after the harvest: the command must print
	// what the vault kept, which it can only do by not reading the host.
	if err := os.WriteFile(host, []byte("rewritten by the host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out string
	stderr := captureStderr(t, func() {
		out = captureStdout(t, func() {
			if err := runTranscript([]string{fixtureSession}); err != nil {
				t.Fatalf("transcript: %v", err)
			}
		})
	})
	if out != string(want) {
		t.Errorf("printed %d bytes that are not the archived transcript (%d bytes):\n%.200s", len(out), len(want), out)
	}
	if !strings.Contains(stderr, "_convos/"+fixtureSession+"/transcript.jsonl") {
		t.Errorf("stderr does not say where the copy was read from: %q", stderr)
	}

	// Gone from the host entirely, the copy still prints.
	if err := os.Remove(host); err != nil {
		t.Fatal(err)
	}
	captureStderr(t, func() {
		out = captureStdout(t, func() {
			if err := runTranscript([]string{fixtureSession}); err != nil {
				t.Errorf("transcript after the host deleted its file: %v", err)
			}
		})
	})
	if out != string(want) {
		t.Error("the archived copy did not print once the host's file was gone")
	}
}

func TestTranscriptCommandSaysNothingIsArchivedAndHowToArchiveIt(t *testing.T) {
	scratchIngest(t)
	err := runTranscript([]string{"no-such-session"})
	if err == nil || !strings.Contains(err.Error(), "no transcript is archived") || !strings.Contains(err.Error(), "--keep-transcripts") {
		t.Errorf("err = %v, want it to say nothing is archived and how to keep one", err)
	}
}

// The default harvest is the one every user consented to: a candidate and
// nothing of the transcript.
func TestAHarvestWithoutKeepingTurnedOnArchivesNothing(t *testing.T) {
	v := scratchIngest(t)
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if strings.Contains(out, "archived") {
		t.Errorf("a default harvest talks about archiving:\n%s", out)
	}
	_ = filepath.WalkDir(filepath.Join(v, "sessions"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && session.IsTranscriptFile(d.Name()) {
			t.Errorf("a default harvest put %s in the vault", p)
		}
		return nil
	})
}

func TestTurningKeepingOnWarnsOnceAndTheHarvestNamesEachCopysSize(t *testing.T) {
	scratchIngest(t)
	ownedHostCopy(t)
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes", "--keep-transcripts"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if !strings.Contains(out, "Store the vault as") {
		t.Errorf("turning keeping on did not warn what the copy is:\n%s", out)
	}
	if !strings.Contains(out, "1 raw transcript(s) archived into the vault (") {
		t.Errorf("the summary does not count and size the copies:\n%s", out)
	}
	if !strings.Contains(out, "(raw, ") {
		t.Errorf("the per-transcript line does not size the copy:\n%s", out)
	}

	again := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes", "--keep-transcripts"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if strings.Contains(again, "Store the vault as") {
		t.Errorf("the warning repeated on a run that changed nothing:\n%s", again)
	}
}
