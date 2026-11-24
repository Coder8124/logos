package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
	"github.com/Coder8124/logos/internal/exclude"
	"github.com/Coder8124/logos/internal/health"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

const codexFixtureID = "01a05124-f309-7cd3-86e6-d0d303880456"

func ingestCodex(t *testing.T) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "codex", "--all-projects", "--yes"}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	})
}

// The first look at a Codex rollout used to read the Claude Code layout, found
// no folder, and so refused every Codex session whenever any project was
// excluded: the protection worked by switching a whole host off.
func TestACodexSessionInAFolderThatIsNotExcludedIsStillReadWhileAProjectIsExcluded(t *testing.T) {
	vaultDir := scratchIngest(t)
	if err := runExclude([]string{"--project", widgetsPath}); err != nil {
		t.Fatal(err)
	}
	out := ingestCodex(t)
	if strings.Contains(out, "session skipped (") || strings.Contains(out, "sessions skipped (") {
		t.Errorf("a Codex session outside the excluded project was skipped:\n%s", out)
	}
	if hits := vaultMentions(t, vaultDir, codexFixtureID); len(hits) == 0 {
		t.Errorf("the Codex session was not harvested:\n%s", out)
	}
}

// The uuid is in the rollout's file name, not its stem, so an exclusion by id
// never matched a Codex file and its text was read.
func TestACodexSessionExcludedByItsUuidIsNotRead(t *testing.T) {
	vaultDir := scratchIngest(t)
	if err := runExclude([]string{"--session", codexFixtureID}); err != nil {
		t.Fatal(err)
	}
	out := ingestCodex(t)
	if !strings.Contains(out, "1 session skipped (excluded session)") {
		t.Errorf("the receipt does not say the Codex session was skipped:\n%s", out)
	}
	if hits := vaultMentions(t, vaultDir, codexFixtureID); len(hits) != 0 {
		t.Errorf("an excluded Codex session left %v", hits)
	}
}

func TestACodexSessionInAnExcludedFolderIsNotRead(t *testing.T) {
	vaultDir := scratchIngest(t)
	if err := runExclude([]string{"--project", "/Users/bob/src/gadgets"}); err != nil {
		t.Fatal(err)
	}
	out := ingestCodex(t)
	if !strings.Contains(out, "1 session skipped (excluded project)") {
		t.Errorf("receipt:\n%s", out)
	}
	if hits := vaultMentions(t, vaultDir, codexFixtureID); len(hits) != 0 {
		t.Errorf("left %v", hits)
	}
}

// writeClaudeChat writes a Claude Code chat that ran from start to end and
// whose file was last written at wrote.
func writeClaudeChat(t *testing.T, root, folder, id, cwd string, start, end, wrote time.Time) string {
	t.Helper()
	dir := filepath.Join(root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(typ string, at time.Time, text string) string {
		role := map[string]string{"user": "user", "assistant": "assistant"}[typ]
		return fmt.Sprintf(`{"type":%q,"sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":%q,"content":%q}}`,
			typ, id, cwd, at.UTC().Format("2006-01-02T15:04:05.000Z"), role, text)
	}
	body := line("user", start, "a question") + "\n" + line("assistant", end, "an answer") + "\n"
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, wrote, wrote); err != nil {
		t.Fatal(err)
	}
	return p
}

func writePauseLine(t *testing.T, vaultDir string, from, until time.Time) {
	t.Helper()
	body := "---\ntype: logos-exclude\n---\n- pause: from=" + from.UTC().Format(time.RFC3339) +
		" until=" + until.UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(vaultDir, exclude.File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A chat that ran 09:00 to 12:00 overlaps a pause from 10:00 to 11:00, but its
// file was last written at 12:00, outside it. Judging by the last write read
// the one chat that was open while the person paused.
func TestAChatThatSpannedAPauseIsSkippedThoughItsFileWasWrittenAfterIt(t *testing.T) {
	vaultDir := scratchIngest(t)
	root := t.TempDir()
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	base := time.Now().Add(-6 * time.Hour).Truncate(time.Second)
	id := "abcdef01-0000-4000-8000-000000000001"
	writeClaudeChat(t, root, "-tmp-spanning", id, "/tmp/spanning", base, base.Add(3*time.Hour), base.Add(3*time.Hour))
	writePauseLine(t, vaultDir, base.Add(time.Hour), base.Add(2*time.Hour))

	out := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "1 session skipped (paused)") {
		t.Errorf("the chat that spanned the pause was not skipped:\n%s", out)
	}
	if strings.Contains(out, "not archived") {
		t.Errorf("a refusal after the read was reported as an archive failure:\n%s", out)
	}
	if hits := vaultMentions(t, vaultDir, id); len(hits) != 0 {
		t.Errorf("a paused chat left %v", hits)
	}
}

// A chat that ended before the pause began is not what the person paused.
func TestAChatThatEndedBeforeAPauseIsStillRead(t *testing.T) {
	vaultDir := scratchIngest(t)
	root := t.TempDir()
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	base := time.Now().Add(-6 * time.Hour).Truncate(time.Second)
	id := "abcdef01-0000-4000-8000-000000000002"
	writeClaudeChat(t, root, "-tmp-before", id, "/tmp/before", base, base.Add(30*time.Minute), base.Add(30*time.Minute))
	writePauseLine(t, vaultDir, base.Add(time.Hour), base.Add(2*time.Hour))
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "skipped (paused)") {
		t.Errorf("a chat that ended before the pause was skipped:\n%s", out)
	}
	if hits := vaultMentions(t, vaultDir, id); len(hits) == 0 {
		t.Errorf("the chat before the pause was not harvested:\n%s", out)
	}
}

// copyTree copies the Claude Code fixtures so a test can add a chat beside them.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	_ = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		b, _ := os.ReadFile(p)
		dst := filepath.Join(to, rel)
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		return os.WriteFile(dst, b, 0o644)
	})
}

// `logos resume` prints the first eight characters of an id; the exclusion
// accepted them but erase wanted the whole id, so the command that said it
// erased a chat erased nothing.
func TestErasingByTheEightCharacterPrefixErasesThatChatsCopies(t *testing.T) {
	vaultDir := scratchIngest(t)
	captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if session.FindTranscript(vaultDir, ecoGameID) == "" {
		t.Fatal("nothing archived to erase")
	}
	out := captureStdout(t, func() {
		if err := runExclude([]string{"--session", ecoGameID[:8], "--erase"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "erased 1 archived transcript(s)") {
		t.Errorf("a prefix erased nothing:\n%s", out)
	}
	if p := session.FindTranscript(vaultDir, ecoGameID); p != "" {
		t.Errorf("the archive is still at %s", p)
	}
}

// A prefix that fits two chats is a guess, and an erase cannot be undone: it
// refuses, lists the chats it fits, and excludes nothing.
func TestErasingByAPrefixThatFitsTwoChatsRefusesAndListsThem(t *testing.T) {
	vaultDir := scratchIngest(t)
	root := t.TempDir()
	copyTree(t, absFixture(t, "claude-code"), root)
	twin := "99999999-0000-4000-8000-000000000009"
	now := time.Now().Add(-2 * time.Hour)
	writeClaudeChat(t, root, "-Users-alice-code-eco-game", twin, "/Users/alice/code/eco-game", now, now.Add(time.Minute), now.Add(time.Minute))
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	var err error
	out := captureStdout(t, func() { err = runExclude([]string{"--session", "99999999", "--erase"}) })
	if err == nil {
		t.Fatalf("an ambiguous erase succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), ecoGameID) || !strings.Contains(err.Error(), twin) {
		t.Errorf("the refusal does not list both chats: %v", err)
	}
	if session.FindTranscript(vaultDir, ecoGameID) == "" || session.FindTranscript(vaultDir, twin) == "" {
		t.Error("an ambiguous erase deleted a chat's copy")
	}
	if got := exclude.CheckID(vaultDir, ecoGameID); got != "" {
		t.Errorf("a refused erase still added an exclusion: %q", got)
	}
}

func TestAnExclusionFileWithABrokenPauseLineIsShownAsBrokenInListAndDoctor(t *testing.T) {
	vaultDir := scratchIngest(t)
	if err := os.WriteFile(filepath.Join(vaultDir, exclude.File),
		[]byte("---\ntype: logos-exclude\n---\n- pause: from=yesterday until=soon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := captureStdout(t, func() {
		if err := runExclude([]string{"--list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(list, "BROKEN") || !strings.Contains(list, "from=yesterday") {
		t.Errorf("--list does not show the broken line:\n%s", list)
	}
	c, ok := pauseCheck(vaultDir, time.Now())
	if !ok || c.State != health.Failed || !strings.Contains(c.Fix, "exclude.md") {
		t.Errorf("doctor's check for a broken line: %+v (shown %v)", c, ok)
	}
	// Nothing is read until it is fixed.
	out := captureStdout(t, func() {
		if err := runIngest([]string{"--harness", "claude-code", "--all-projects", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "0 queued") || !strings.Contains(out, "skipped") {
		t.Errorf("a broken exclusion list still let sessions be read:\n%s", out)
	}
}

// A hook records every tool call. For a session the person excluded, the
// command log and a saved plan would still hold what the transcript no longer
// does.
func TestAnExcludedSessionWritesNoActivityRowsAndNoSavedPlan(t *testing.T) {
	vaultDir := standIn(t, "shop")
	if err := runExclude([]string{"--session", "s-excl-0002"}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-excl-0002","tool_name":"Bash","tool_input":{"command":"echo quokka-secret"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-excl-0002","tool_name":"ExitPlanMode","tool_input":{"plan":"ship the quokka plan"}}`)
	rows, err := activity.Read(vaultDir, activity.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("an excluded session wrote %d activity row(s)", len(rows))
	}
	if hits := vaultMentions(t, vaultDir, "quokka"); len(hits) != 0 {
		t.Errorf("an excluded session saved %v", hits)
	}
}

func TestAnExcludedProjectWritesNoActivityRows(t *testing.T) {
	vaultDir := standIn(t, "shop")
	cwd, _ := os.Getwd()
	if err := runExclude([]string{"--project", cwd}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", fmt.Sprintf(`{"session_id":"s-proj-0001","cwd":%q,"tool_name":"Bash","tool_input":{"command":"ls"}}`, cwd))
	rows, _ := activity.Read(vaultDir, activity.Query{})
	if len(rows) != 0 {
		t.Errorf("an excluded project wrote %d activity row(s)", len(rows))
	}
}

func TestAPauseStopsActivityRowsWhileItLasts(t *testing.T) {
	vaultDir := standIn(t, "shop")
	captureStdout(t, func() {
		if err := runPause(nil); err != nil {
			t.Fatal(err)
		}
	})
	hookEvent(t, "PostToolUse", `{"session_id":"s-pause-0001","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if rows, _ := activity.Read(vaultDir, activity.Query{}); len(rows) != 0 {
		t.Errorf("a paused session wrote %d activity row(s)", len(rows))
	}
	captureStdout(t, func() { _ = runPause([]string{"resume"}) })
	hookEvent(t, "PostToolUse", `{"session_id":"s-after-0001","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if rows, _ := activity.Read(vaultDir, activity.Query{}); len(rows) != 1 {
		t.Errorf("after resume, %d activity rows; want 1", len(rows))
	}
}

func TestExcludingSaysActivityAndPlansAreSkippedToo(t *testing.T) {
	standIn(t, "shop")
	out := captureStdout(t, func() {
		if err := runExclude([]string{"--session", "s-excl-0003"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "activity") || !strings.Contains(out, "plan") {
		t.Errorf("the output does not say activity rows and plans are skipped:\n%s", out)
	}
}
