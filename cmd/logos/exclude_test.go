package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/exclude"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

const (
	widgetsID   = "11111111-2222-3333-4444-555555555555"
	ecoGameID   = "99999999-8888-7777-6666-555555555555"
	widgetsPath = "/Users/alice/code/widgets"
)

// vaultMentions lists the vault files whose name or contents carry id, so a
// test can say "nothing of that chat was kept" without knowing which folder
// a checkpoint, candidate or archive would have been filed under.
func vaultMentions(t *testing.T, vaultDir, id string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(vaultDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(p, string(filepath.Separator)+".logos"+string(filepath.Separator)) ||
			filepath.Base(p) == exclude.File {
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(p, id) || strings.Contains(string(b), id) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

func TestAnExcludedProjectLeavesNoArchiveCheckpointOrMemory(t *testing.T) {
	vaultDir := scratchIngest(t)
	if err := runExclude([]string{"--project", widgetsPath}); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if hits := vaultMentions(t, vaultDir, widgetsID); len(hits) != 0 {
		t.Errorf("an excluded project's chat left %v", hits)
	}
	// The other project's chat was harvested, so the check above is not a
	// vault that kept nothing at all.
	if hits := vaultMentions(t, vaultDir, ecoGameID); len(hits) == 0 {
		t.Error("the project that was not excluded was not harvested either")
	}
}

func TestASkippedSessionIsCountedInTheReceipt(t *testing.T) {
	scratchIngest(t)
	if err := runExclude([]string{"--project", widgetsPath}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
	if !strings.Contains(out, "1 session skipped (excluded project)") {
		t.Errorf("the receipt does not say what was skipped:\n%s", out)
	}
}

func TestExcludingAProjectSaysWhatIsAlreadyKeptAndHowToRemoveIt(t *testing.T) {
	scratchIngest(t)
	captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	out := captureStdout(t, func() {
		if err := runExclude([]string{"--session", ecoGameID}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "already kept: 1 archived transcript(s)") || !strings.Contains(out, "--erase") {
		t.Errorf("excluding did not say what is kept, or how to remove it:\n%s", out)
	}
}

func TestEraseRemovesTheChatsArchiveAndCheckpointsAndSaysTheCounts(t *testing.T) {
	vaultDir := scratchIngest(t)
	captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if session.FindTranscript(vaultDir, ecoGameID) == "" {
		t.Fatal("the fixture chat was not archived, so there is nothing to erase")
	}
	out := captureStdout(t, func() {
		if err := runExclude([]string{"--session", ecoGameID, "--erase"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "erased 1 archived transcript(s)") {
		t.Errorf("the erase did not say its counts:\n%s", out)
	}
	if p := session.FindTranscript(vaultDir, ecoGameID); p != "" {
		t.Errorf("the archived transcript is still at %s", p)
	}
	if cps, _ := session.ByHostSession(vaultDir, ecoGameID); len(cps) != 0 {
		t.Errorf("%d checkpoint(s) of the erased chat remain", len(cps))
	}
	// A later sweep must not bring it back: the exclusion stays.
	if got := exclude.CheckID(vaultDir, ecoGameID); got != exclude.ReasonSession {
		t.Errorf("the erased chat is no longer excluded: %q", got)
	}
	// Index second: it was synced after the vault changed, so it holds no row
	// for what is gone.
	ix, err := openIndexQuiet()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if cps, _ := session.ByHostSession(vaultDir, ecoGameID); len(cps) != 0 {
		t.Errorf("the index still lists %d checkpoint(s) for the erased chat", len(cps))
	}
}

func TestPausingSkipsSessionsActiveNowAndSaysHowMany(t *testing.T) {
	scratchIngest(t)
	// The checked-in fixtures carry the checkout's file times, not this
	// moment's, so the chats are copied to be as fresh as a live one.
	fresh := filepath.Join(t.TempDir(), "projects")
	_ = filepath.WalkDir(absFixture(t, "claude-code"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(absFixture(t, "claude-code"), p)
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatal(rerr)
		}
		dst := filepath.Join(fresh, rel)
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(dst, b, 0o644); werr != nil {
			t.Fatal(werr)
		}
		return nil
	})
	t.Setenv(transcript.LogosClaudeProjectsEnv, fresh)
	out := captureStdout(t, func() {
		if err := runPause([]string{"2h"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "paused until") {
		t.Errorf("pausing did not say until when:\n%s", out)
	}
	// The fixtures are files written moments ago, so they fall inside it.
	got := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "2 sessions skipped (paused)") {
		t.Errorf("the harvest did not count what the pause skipped:\n%s", got)
	}
	if !strings.Contains(got, "0 queued") {
		t.Errorf("a paused harvest queued something:\n%s", got)
	}
}

func TestPauseResumeEndsItAndTheListAndDoctorShowItWhileItLasts(t *testing.T) {
	vaultDir := scratchIngest(t)
	captureStdout(t, func() {
		if err := runPause(nil); err != nil {
			t.Fatal(err)
		}
	})
	list := captureStdout(t, func() {
		if err := runExclude([]string{"--list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(list, "paused until") {
		t.Errorf("--list does not show the pause:\n%s", list)
	}
	c, ok := pauseCheck(vaultDir, time.Now())
	if !ok || !strings.Contains(c.Detail, "not read until") || c.Fix != "logos pause resume" {
		t.Errorf("doctor's pause line: %+v (shown %v)", c, ok)
	}
	out := captureStdout(t, func() {
		if err := runPause([]string{"resume"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "pause ended") {
		t.Errorf("resume did not say it ended the pause:\n%s", out)
	}
	if _, ok := pauseCheck(vaultDir, time.Now().Add(time.Second)); ok {
		t.Error("doctor still reports a pause after it was ended")
	}
}

func TestAnExcludedSessionEndingLeavesNoAutoCheckpoint(t *testing.T) {
	standIn(t, "elsewhere")
	hookEvent(t, "PostToolUse", `{"session_id":"s-excl-0001","tool_name":"Edit","tool_input":{"file_path":"cart.go"}}`)
	if err := runExclude([]string{"--session", "s-excl-0001"}); err != nil {
		t.Fatal(err)
	}
	out := endSession(t, "s-excl-0001")
	if !strings.Contains(out, "1 session skipped (excluded session)") {
		t.Errorf("the session end did not say it skipped:\n%s", out)
	}
	if c, _ := session.Latest(vaultPath(), "shop"); c != nil {
		t.Errorf("an excluded session was checkpointed: %+v", c)
	}
}
