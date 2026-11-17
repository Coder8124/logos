package main

import (
	"github.com/Coder8124/logos/internal/auto"
	"github.com/Coder8124/logos/internal/health"
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

func TestTranscriptCommandSaysNothingIsArchivedAndHowArchivingIsSwitched(t *testing.T) {
	scratchIngest(t)
	err := runTranscript([]string{"no-such-session"})
	if err == nil || !strings.Contains(err.Error(), "no transcript is archived") || !strings.Contains(err.Error(), "archive off") {
		t.Errorf("err = %v, want it to say nothing is archived and name the archive switch", err)
	}
}

// Archiving is on by default, so the harvest that archives nothing is the one
// of a user who turned the archive switch off: a candidate and nothing of the
// transcript.
func TestAHarvestWithArchiveTurnedOffArchivesNothing(t *testing.T) {
	v := scratchIngest(t)
	if err := auto.Set(v, "archive", false); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if strings.Contains(out, "archived") {
		t.Errorf("a harvest with archive off talks about archiving:\n%s", out)
	}
	_ = filepath.WalkDir(filepath.Join(v, "sessions"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && session.IsTranscriptFile(d.Name()) {
			t.Errorf("a harvest with archive off put %s in the vault", p)
		}
		return nil
	})
}

func TestTheFirstHarvestSaysTranscriptsAreKeptOnceAndNamesEachCopysSize(t *testing.T) {
	scratchIngest(t)
	ownedHostCopy(t)
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if !strings.Contains(out, "Logos now keeps a copy of each transcript") {
		t.Errorf("the first archive did not say what the copy is:\n%s", out)
	}
	if !strings.Contains(out, "1 transcript(s) archived into the vault (") {
		t.Errorf("the summary does not count and size the copies:\n%s", out)
	}
	if strings.Contains(out, ", raw") {
		t.Errorf("a redacted copy was reported as raw:\n%s", out)
	}

	again := captureStdout(t, func() {
		if err := runIngest([]string{"--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if strings.Contains(again, "Logos now keeps a copy of each transcript") {
		t.Errorf("the notice repeated on a run that changed nothing:\n%s", again)
	}
}

// A copy kept while redact was off holds whatever the tool printed. Doctor
// counts it, the command rewrites it and says how many, and doctor then stops
// counting it.
func TestDoctorCountsRawCopiesAndTranscriptRedactRewritesThem(t *testing.T) {
	v := scratchIngest(t)
	token := "ghp_" + strings.Repeat("a1B2c3", 6)
	dir := filepath.Join(v, "sessions", "widgets", "_convos", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"user","message":{"role":"user","content":"token ` + token + `"}}` + "\n"
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := rawTranscriptsCheck(v); c.State == health.OK || c.Fix != "logos transcript redact" {
		t.Fatalf("doctor check = %+v, want a warning with the fix", c)
	}
	out := captureStdout(t, func() {
		if err := runTranscript([]string{"redact"}); err != nil {
			t.Errorf("transcript redact: %v", err)
		}
	})
	if !strings.Contains(out, "1 archived transcript(s) redacted") {
		t.Errorf("redact did not print the count:\n%s", out)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), token) {
		t.Error("the copy still holds the token")
	}
	if c := rawTranscriptsCheck(v); c.State != health.OK {
		t.Errorf("doctor still warns after redacting: %+v", c)
	}
}
