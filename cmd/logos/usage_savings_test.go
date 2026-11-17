package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// savingsEnv is a scratch vault with the host transcript readers pointed at
// temp directories, so nothing here reads or writes a real machine's files.
type savingsEnv struct {
	vault, claude string
	now           time.Time
}

func newSavingsEnv(t *testing.T) savingsEnv {
	t.Helper()
	t.Setenv("LOGOS_EMBED", "off")
	e := savingsEnv{vault: t.TempDir(), claude: t.TempDir(), now: time.Now()}
	t.Setenv(transcript.LogosClaudeProjectsEnv, e.claude)
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())
	return e
}

func (e savingsEnv) transcript(t *testing.T, name string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.claude, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e savingsEnv) checkpoint(t *testing.T, project, host string, ago time.Duration) {
	t.Helper()
	commitAt(t, e.vault, session.Checkpoint{Project: project, Agent: "a", Task: "t " + project, Next: "n", HostSession: host, TS: e.now.Add(-ago).Unix()})
}

func (e savingsEnv) restore(project, readerSession string, ago time.Duration) usagepkg.Event {
	return usagepkg.Event{TS: e.now.Add(-ago).Unix(), Kind: usagepkg.KindPack, Project: project, Via: "cli:resume", Sent: 1_500, Full: 1_500, Session: readerSession}
}

func TestUnreadableTranscriptsAreReportedAsAnErrorNotCalledMissing(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "kestrel", "sess-a", 2*time.Hour)
	if err := os.Chmod(e.claude, 0); err != nil {
		t.Skip("cannot make a directory unreadable:", err)
	}
	t.Cleanup(func() { os.Chmod(e.claude, 0o755) })
	if _, err := os.ReadDir(e.claude); err == nil {
		t.Skip("running as a user that can read a mode 0 directory")
	}
	r := measureRestores(e.vault, []usagepkg.Event{e.restore("kestrel", "", time.Hour)}, "")
	if r.Err == nil || r.Unread != 1 || r.NoTranscript != 0 {
		t.Fatalf("restores = %+v, want the failure kept as Err and the restore counted unread, not as having no transcript", r)
	}
	d := sampleDash()
	d.restores = r
	out := strings.Join(d.savedPanel(100), "\n")
	if !strings.Contains(out, "error: reading the hosts' transcripts") || strings.Contains(out, "no transcript on this machine") {
		t.Fatalf("the panel does not report the read failure as one:\n%s", out)
	}
}

func TestARestoreInsideTheSessionThatWroteTheCheckpointIsNotCredited(t *testing.T) {
	e := newSavingsEnv(t)
	const host = "abcd1234-0000-4000-8000-000000000000"
	e.checkpoint(t, "kestrel", host, 2*time.Hour)
	e.transcript(t, "s", claudeLine(e.now.Add(-3*time.Hour), host, 200_000))
	// The ledger keeps the reader's session as an 8-character cut.
	r := measureRestores(e.vault, []usagepkg.Event{e.restore("kestrel", "abcd1234", time.Hour)}, "")
	if r.Measured != 0 || r.Saved != 0 || r.SameSession != 1 {
		t.Fatalf("restores = %+v, want the same-session resume counted apart and nothing saved: that session never reloaded", r)
	}
	if got := strings.Join(r.notMeasured(), "\n"); !strings.Contains(got, "1 restore resumed inside the session that wrote the checkpoint") {
		t.Fatalf("the line does not say so:\n%s", got)
	}
}

func TestTheStartHookAndTheAgentsOwnResumeInOneSessionAreOneRestore(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "kestrel", "sess-big", 3*time.Hour)
	e.transcript(t, "s", claudeLine(e.now.Add(-4*time.Hour), "sess-big", 200_000))
	events := []usagepkg.Event{
		e.restore("kestrel", "aaaaaaaa", 2*time.Hour),
		e.restore("kestrel", "aaaaaaaa", 119*time.Minute),
		e.restore("kestrel", "bbbbbbbb", time.Hour),
	}
	r := measureRestores(e.vault, events, "")
	if r.Measured != 2 || r.Repeats != 1 || r.Saved != 2*(200_000-1_500) {
		t.Fatalf("restores = %+v, want one credit per reader session (two) and the repeat said once", r)
	}
}

func TestAWorktreesRestoreIsMeasuredAgainstItsOwnCheckpointNotItsProjects(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "kestrel", "sess-proj", 3*time.Hour)
	e.checkpoint(t, "kestrel/feat", "sess-tree", 3*time.Hour)
	e.transcript(t, "s",
		claudeLine(e.now.Add(-4*time.Hour), "sess-proj", 100_000),
		claudeLine(e.now.Add(-4*time.Hour), "sess-tree", 300_000))
	r := measureRestores(e.vault, []usagepkg.Event{
		e.restore("kestrel", "aaaaaaaa", 2*time.Hour),
		e.restore("kestrel/feat", "bbbbbbbb", time.Hour),
	}, "")
	if r.Measured != 2 || r.Saved != (100_000-1_500)+(300_000-1_500) {
		t.Fatalf("restores = %+v, want the worktree measured against its own 300K session, not its project's 100K", r)
	}
}

func TestContextAddedAfterTheCheckpointIsNotTheRestoresBaseline(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "kestrel", "sess-big", 2*time.Hour)
	e.transcript(t, "s",
		claudeLine(e.now.Add(-5*time.Hour), "sess-big", 100_000),
		claudeLine(e.now.Add(-30*time.Minute), "sess-big", 400_000)) // the session went on after the checkpoint
	r := measureRestores(e.vault, []usagepkg.Event{e.restore("kestrel", "", time.Hour)}, "")
	if r.Measured != 1 || r.Baseline != 100_000 {
		t.Fatalf("restores = %+v, want the 100K the session had reached by its checkpoint, not the 400K it grew to later", r)
	}
}

func TestEachReasonARestoreWentUnmeasuredIsNamedApart(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "nohost", "", 2*time.Hour)
	e.checkpoint(t, "cutid", "abcd1234", 2*time.Hour)
	e.checkpoint(t, "notrans", "sess-gone", 2*time.Hour)
	e.transcript(t, "s", claudeLine(e.now.Add(-4*time.Hour), "sess-else", 1_000))
	r := measureRestores(e.vault, []usagepkg.Event{
		e.restore("ghost", "", time.Hour), // no checkpoint at all
		e.restore("nohost", "", time.Hour),
		e.restore("cutid", "", time.Hour),
		e.restore("notrans", "", time.Hour),
	}, "")
	if r.NoCheckpoint != 1 || r.NoHostSession != 2 || r.NoTranscript != 1 {
		t.Fatalf("restores = %+v, want one with no checkpoint, two with no usable host session, one with no transcript", r)
	}
	lines := r.notMeasured()
	if len(lines) != 3 {
		t.Fatalf("lines = %q, want one per non-zero reason and no zero counts", lines)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"1 restore had no checkpoint before it", "2 restores had a checkpoint that names no host session", "1 restore had no transcript on this machine"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Count(got, "on this machine") != 1 {
		t.Errorf("only the missing-transcript reason is about this machine:\n%s", got)
	}
}

func TestThePipedReceiptMeasuresRestoresOverTheLastThirtyDaysOnly(t *testing.T) {
	e := newSavingsEnv(t)
	e.checkpoint(t, "kestrel", "sess-big", 2*time.Hour)
	e.transcript(t, "s", claudeLine(e.now.Add(-4*time.Hour), "sess-big", 200_000))
	old := e.restore("kestrel", "", 90*24*time.Hour)
	for _, ev := range []usagepkg.Event{old, e.restore("kestrel", "", time.Hour)} {
		if err := usagepkg.Record(e.vault, ev); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOGOS_VAULT", e.vault)
	out := captureStdout(t, func() {
		if err := runUsage(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "restores in the last 30 days spared") || !strings.Contains(out, "across 1 restore,") {
		t.Fatalf("the receipt does not bound the restore measure to 30 days:\n%s", out)
	}
	if strings.Contains(out, "no checkpoint before it") {
		t.Fatalf("a restore from 90 days ago was measured:\n%s", out)
	}
}
