package ingest

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"

	_ "modernc.org/sqlite"
)

// endedAgo is worked() as a session that ran for twenty minutes and ended d ago.
func endedAgo(now time.Time, d time.Duration) *transcript.Session {
	s := worked()
	s.Ended = now.Add(-d).Unix()
	s.Started = now.Add(-d - 20*time.Minute).Unix()
	return s
}

// onMachine makes these the only transcripts the sweep can see.
func onMachine(t *testing.T, found ...*transcript.Session) {
	t.Helper()
	was := RecentTranscripts
	t.Cleanup(func() { RecentTranscripts = was })
	RecentTranscripts = func(_ string, since, until time.Time, _ func(string, time.Time) bool) ([]*transcript.Session, []string) {
		return found, nil
	}
}

func sessionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := session.Init(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// The shutdown path runs from a deferred call, so a host that was killed, or a
// server that never started, recorded nothing — and the transcript on disk was
// never read again (#134). The next resume reads it.
func TestASessionKilledBeforeItCouldBeRecordedIsRecordedByTheNextResume(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	lost := endedAgo(now, 2*time.Hour)
	onMachine(t, lost)

	wrote, _, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(wrote) != 1 {
		t.Fatalf("recorded %d sessions, want the one that ended without a checkpoint", len(wrote))
	}
	c := wrote[0]
	if !c.Auto || len(c.Verified) != 0 || len(c.Failed) != 0 || c.Next != "" {
		t.Errorf("the sweep's record claims more than an auto record may: %+v", c)
	}
	// Dated when the session ended, not when it was found: this is what keeps
	// it from reading as the newest thing that happened.
	if c.TS != lost.Ended {
		t.Errorf("recorded at %d, want the session's own end %d", c.TS, lost.Ended)
	}
	if !c.Git.Empty() {
		t.Errorf("the repository now is not the one that session left, but its git state was read: %+v", c.Git)
	}
	latest, _ := session.Latest(vault, "shop")
	if latest == nil || latest.Session != c.Session {
		t.Errorf("the record is not on disk where resume reads it: %+v", latest)
	}
}

// Dated now, a list of files from a session hours old would sit above the
// reviewed handoff somebody wrote after it, and resume would lead with it.
func TestASweptSessionDoesNotOutrankTheCheckpointWrittenAfterIt(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	db := sessionDB(t)
	real := &session.Checkpoint{Project: "shop", Agent: "claude-code", Task: "the real handoff", Next: "ship it"}
	if err := session.Commit(db, vault, real); err != nil {
		t.Fatal(err)
	}
	// Ended before the real checkpoint, and outside its span, so nothing
	// covers it.
	onMachine(t, endedAgo(now, 3*time.Hour))

	wrote, _, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(wrote) != 1 {
		t.Fatalf("wrote %d, problems %v", len(wrote), problems)
	}
	latest, err := session.Latest(vault, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Session != real.Session {
		t.Errorf("latest is %+v, want the reviewed handoff written after the swept session", latest)
	}
	raw, err := os.ReadFile(filepath.Join(vault, wrote[0].Slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "[["+real.Session+"]]") {
		t.Errorf("the swept record claims to follow a checkpoint written after it:\n%s", raw)
	}
}

// The Claude Code session-end hook's records did not always say which session
// they came from. One written as the session closed is still that session's,
// or the first resume after an upgrade would record a week of hooked sessions
// a second time.
func TestASessionTheHookRecordedBeforeItNamedSessionsIsNotSweptAgain(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	s.Harness = "claude-code"
	if _, err := session.WriteAuto(vault, session.Checkpoint{
		Project: "shop", Agent: "claude-code", Files: []string{"x.go"},
		TS: s.Ended + 60, // the hook, a minute after the window closed
	}); err != nil {
		t.Fatal(err)
	}
	onMachine(t, s)

	wrote, _, problems := Sweep(vault, "shop", now)
	if len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("a session that already had a checkpoint was swept: wrote %+v, problems %v", wrote, problems)
	}
}

// Every resume sweeps. One lost session must become one record however many
// resumes follow it.
func TestSweepingTwiceRecordsASessionOnce(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	onMachine(t, endedAgo(now, 2*time.Hour))

	Sweep(vault, "shop", now)
	wrote, _, _ := Sweep(vault, "shop", now.Add(time.Minute))
	if len(wrote) != 0 {
		t.Errorf("the second resume recorded the same session again: %+v", wrote)
	}
	all, _ := session.History(vault, "shop", 0)
	if len(all) != 1 {
		t.Errorf("%d checkpoints on disk, want 1", len(all))
	}
}

// The session calling resume is writing a transcript as it does, and a second
// window may still be open. Neither has ended.
func TestASessionThatMayStillBeRunningIsLeftAlone(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	onMachine(t, endedAgo(now, 5*time.Minute))

	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 0 {
		t.Errorf("a transcript written five minutes ago was recorded as a finished session: %+v", wrote)
	}
}

// Resume on one project must not file another project's sessions under it, or
// under their own project behind the user's back.
func TestASessionFromAnotherProjectIsNotSwept(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	other := endedAgo(now, 2*time.Hour)
	other.Project = "kestrel"
	onMachine(t, other)

	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 0 {
		t.Errorf("resume on shop recorded a kestrel session: %+v", wrote)
	}
	if all, _ := session.History(vault, "kestrel", 0); len(all) != 0 {
		t.Errorf("resume on shop wrote into kestrel: %+v", all)
	}
}

// The shutdown path and the sweep can both see one transcript. Whichever runs
// second must recognise the first one's record.
func TestASessionTheShutdownPathRecordedIsNotSweptAgain(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	if c, err := AutoCheckpoint(vault, s, "shop"); err != nil || c == nil {
		t.Fatalf("shutdown path: %+v, %v", c, err)
	}
	onMachine(t, s)

	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 0 {
		t.Errorf("the sweep recorded a session the shutdown path already had: %+v", wrote)
	}
}

// A host touches old transcripts long after they end — Claude Code rewrote a
// batch of week-old files in one minute here — so a file written yesterday can
// hold a session from last month. That one is the review queue's, not a
// checkpoint to put into a history it predates.
func TestASessionThatEndedBeforeTheWindowIsNotSweptBecauseItsFileWasTouched(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	onMachine(t, endedAgo(now, 30*24*time.Hour))

	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 0 {
		t.Errorf("a month-old session was recorded because its file was touched: %+v", wrote)
	}
}

// onDisk puts one Claude Code transcript of shop, from start to end, where the
// real listing finds it, with a file time that is not the session's: a host
// touches old transcripts. withTool gives it a command the record can list.
func onDisk(t *testing.T, now, start, end time.Time, withTool bool) string {
	t.Helper()
	// A folder of its own, as ~/.claude/projects is, so a test can put
	// Claude Code's sessions/ beside it.
	root := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	t.Setenv(transcript.LogosCursorStorageEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())

	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	lines := []string{
		`{"type":"user","sessionId":"lost-1","cwd":"/work/shop","timestamp":"` + stamp(start) + `","message":{"role":"user","content":"fix the checkout crash"}}`,
	}
	if withTool {
		lines = append(lines,
			`{"type":"assistant","sessionId":"lost-1","cwd":"/work/shop","timestamp":"`+stamp(start)+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./cart"}}]}}`,
			`{"type":"user","sessionId":"lost-1","cwd":"/work/shop","timestamp":"`+stamp(end)+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}`)
	} else {
		lines = append(lines,
			`{"type":"assistant","sessionId":"lost-1","cwd":"/work/shop","timestamp":"`+stamp(end)+`","message":{"role":"assistant","content":"Which crash?"}}`)
	}
	dir := filepath.Join(root, "-work-shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lost-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := now.Add(-90 * time.Minute)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	return path
}

// unreadable makes reading path again a reported problem, which is how these
// tests see that a sweep did not.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
}

// Through the real listing: a Claude Code transcript on disk is found, read and
// recorded, and a resume after that does not read it again. Every resume used
// to reparse a recorded session, which for one 85 MB transcript here was a
// second each time.
func TestASweptTranscriptIsFoundOnDiskAndNotReadAgain(t *testing.T) {
	now := time.Now()
	end := now.Add(-2 * time.Hour)
	path := onDisk(t, now, now.Add(-3*time.Hour), end, true)

	vault := t.TempDir()
	wrote, _, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(wrote) != 1 {
		t.Fatalf("first resume: wrote %d, problems %v", len(wrote), problems)
	}
	if wrote[0].TS != end.Unix() {
		t.Errorf("recorded at %d, want the transcript's last turn %d, not its file time", wrote[0].TS, end.Unix())
	}

	unreadable(t, path)
	wrote, _, problems = Sweep(vault, "shop", now)
	if len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("second resume read a transcript it had already recorded: wrote %+v, problems %v", wrote, problems)
	}
}

// Most transcripts a sweep reads give no record — nothing was run, or another
// checkpoint covers them — so nothing in the vault says they were read. Here
// seven of them, one 16 MB, were reparsed on every resume, which cost a quarter
// of a second each time on a hook with a ten-second limit.
func TestATranscriptThatGaveNoRecordIsNotReadAgain(t *testing.T) {
	now := time.Now()
	path := onDisk(t, now, now.Add(-3*time.Hour), now.Add(-2*time.Hour), false)

	vault := t.TempDir()
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 0 || len(problems) != 0 {
		t.Fatalf("first resume: wrote %+v, problems %v", wrote, problems)
	}
	unreadable(t, path)
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("second resume read a transcript it had already judged: wrote %+v, problems %v", wrote, problems)
	}
}

// Judged for one project is not judged for another: a Codex path says nothing
// of whose session it holds, so a transcript that was not kestrel's must still
// be read when shop resumes.
func TestATranscriptJudgedForOneProjectIsStillReadForAnother(t *testing.T) {
	root := t.TempDir()
	t.Setenv(transcript.LogosClaudeProjectsEnv, t.TempDir())
	t.Setenv(transcript.LogosCursorStorageEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, root)

	now := time.Now()
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	start, end := stamp(now.Add(-3*time.Hour)), stamp(now.Add(-2*time.Hour))
	lines := []string{
		`{"type":"session_meta","timestamp":"` + start + `","payload":{"id":"lost-2","cwd":"/work/shop"}}`,
		`{"type":"response_item","timestamp":"` + start + `","payload":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{\"command\":\"go test ./cart\"}"}}`,
		`{"type":"response_item","timestamp":"` + end + `","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
	}
	dir := filepath.Join(root, "2026", "09", "23")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-23T10-00-00-lost-2.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := now.Add(-90 * time.Minute)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}

	vault := t.TempDir()
	if wrote, _, problems := Sweep(vault, "kestrel", now); len(wrote) != 0 || len(problems) != 0 {
		t.Fatalf("kestrel's resume: wrote %+v, problems %v", wrote, problems)
	}
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 1 || len(problems) != 0 {
		t.Errorf("shop's session was skipped after kestrel's resume read it: wrote %+v, problems %v", wrote, problems)
	}
}

// Deleting .logos loses the cache, not a record: the next resume reads the
// transcripts again and writes nothing it already wrote.
func TestLosingTheSweepCacheCostsAReadNotASecondRecord(t *testing.T) {
	now := time.Now()
	onDisk(t, now, now.Add(-3*time.Hour), now.Add(-2*time.Hour), true)
	vault := t.TempDir()
	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 1 {
		t.Fatalf("first resume wrote %d", len(wrote))
	}
	if err := os.RemoveAll(filepath.Join(vault, ".logos")); err != nil {
		t.Fatal(err)
	}
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("after the cache went: wrote %+v, problems %v", wrote, problems)
	}
}
