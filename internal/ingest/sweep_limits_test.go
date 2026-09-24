package ingest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/transcript"
)

// agentCheckpointAt puts an agent's own checkpoint in the vault, dated at, as
// a second window on the project would have written it.
func agentCheckpointAt(t *testing.T, vault string, at int64, task string) session.Checkpoint {
	t.Helper()
	c := session.Checkpoint{Project: "shop", Agent: "claude-code", Task: task, Next: "ship it", TS: at}
	id := time.Unix(at, 0).Format("20060102-150405") + "-claude-code"
	dir := filepath.Join(vault, session.CheckpointDir, "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(c.Markdown("")), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Session = id
	return c
}

// A checkpoint says nothing of which transcript it came from, so the sweep took
// any one dated inside a session as that session's own. Two windows open on
// one project is ordinary, and the one that checkpointed hid the one that was
// killed: its work was recorded by nobody.
func TestACheckpointFromAnotherWindowDoesNotHideALostSession(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	lost := endedAgo(now, 2*time.Hour)
	agentCheckpointAt(t, vault, lost.Ended-5*60, "the other window's handoff")
	onMachine(t, lost)

	wrote, _, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(wrote) != 1 {
		t.Errorf("a lost session was hidden by another window's checkpoint: wrote %+v, problems %v", wrote, problems)
	}
}

// An agent that checkpointed at noon and worked until six handed off noon's
// work, not the afternoon's. Taking the checkpoint as covering the session lost
// everything after it.
func TestWorkAfterASessionsOwnCheckpointIsRecorded(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	s.Turns = []transcript.Turn{
		{Role: "user", Text: "fix the checkout crash"},
		{Role: "tool", Tool: "edit_file", Input: "internal/cart/checkout.go"},
		{Role: "tool", Tool: "mcp__logos__checkpoint", Input: `{"project":"shop"}`, Status: "ok",
			Text: "✓ Logos · checkpoint saved to logos — sessions/shop/20260923-101500-claude-code.md"},
		{Role: "tool", Tool: "edit_file", Input: "internal/cart/refund.go"},
		{Role: "tool", Tool: "run_terminal_cmd", Input: "go test ./internal/cart", Status: "ok"},
	}
	agentCheckpointAt(t, vault, s.Started+5*60, "fixed the checkout crash")
	onMachine(t, s)

	wrote, _, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(wrote) != 1 {
		t.Fatalf("the work after the session's checkpoint was not recorded: wrote %+v, problems %v", wrote, problems)
	}
	files := strings.Join(wrote[0].Files, " ")
	if !strings.Contains(files, "refund.go") {
		t.Errorf("the record is missing the work after the checkpoint: %v", wrote[0].Files)
	}
	if strings.Contains(files, "checkout.go") {
		t.Errorf("the record lists again what the agent already handed off: %v", wrote[0].Files)
	}
}

// Another server's tool that happens to be called checkpoint handed nothing to
// Logos. Taking it for one hid everything the session did before it.
func TestAnotherServersCheckpointToolDoesNotHideTheWorkBeforeIt(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "mcp__notebook__save_checkpoint",
		Input: `{"path":"model.ipynb"}`, Status: "ok", Text: "Saved checkpoint 3 of model.ipynb"})
	onMachine(t, s)

	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 1 || len(problems) != 0 {
		t.Errorf("another server's checkpoint tool was taken for a Logos handoff: wrote %+v, problems %v", wrote, problems)
	}
}

// Its own checkpoint as the last thing it did is a session handed off in full.
func TestASessionThatEndedOnItsOwnCheckpointIsNotSwept(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 2*time.Hour)
	s.Turns = append(s.Turns, transcript.Turn{Role: "tool", Tool: "checkpoint", Input: `{"project":"shop"}`, Status: "ok"})
	onMachine(t, s)

	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("a session that handed off its work was recorded again: wrote %+v, problems %v", wrote, problems)
	}
}

// Each host names an MCP tool its own way, and the server's part of the name is
// the user's choice; a checkpoint that failed handed nothing off.
func TestACheckpointCallIsRecognisedUnderEveryHostsNameForIt(t *testing.T) {
	for _, tc := range []struct {
		turn transcript.Turn
		want bool
	}{
		{transcript.Turn{Role: "tool", Tool: "mcp__brain__checkpoint"}, true},
		{transcript.Turn{Role: "tool", Tool: "mcp__plugin_logos_logos__handoff"}, true},
		{transcript.Turn{Role: "tool", Tool: "checkpoint"}, true},
		{transcript.Turn{Role: "tool", Tool: "mcp_logos_checkpoint"}, true},
		{transcript.Turn{Role: "tool", Tool: "logos.checkpoint"}, true},
		{transcript.Turn{Role: "tool", Tool: "run_terminal_cmd", Input: "logos checkpoint shop --task x"}, true},
		{transcript.Turn{Role: "tool", Tool: "mcp__brain__checkpoint", Status: "error"}, false},
		{transcript.Turn{Role: "tool", Tool: "edit_file", Input: "internal/session/checkpoint.go"}, false},
		{transcript.Turn{Role: "tool", Tool: "mcp__brain__checkpoints"}, false},
		{transcript.Turn{Role: "user", Text: "please checkpoint"}, false},
		{transcript.Turn{Role: "tool", Tool: "mcp__plugin_logos_logos__checkpoint", Text: "✓ Logos · checkpoint saved to logos — sessions/shop/20260923-101500-claude-code.md"}, true},
		{transcript.Turn{Role: "tool", Tool: "mcp__brain__checkpoint", Text: "Checkpoint written to sessions/shop/20260801-101500-claude-code.md in the vault."}, true},
		{transcript.Turn{Role: "tool", Tool: "run_terminal_cmd", Input: "logos checkpoint shop --task x", Text: "checkpoint written: sessions/shop/20260923-101500-agent.md"}, true},
		{transcript.Turn{Role: "tool", Tool: "mcp__notebook__save_checkpoint", Text: "Saved checkpoint 3 of model.ipynb"}, false},
		{transcript.Turn{Role: "tool", Tool: "run_terminal_cmd", Input: "logos checkpoint --help", Text: "usage: logos checkpoint <project> [flags]"}, false},
	} {
		if got := isHandoffCall(tc.turn); got != tc.want {
			t.Errorf("isHandoffCall(%+v) = %v, want %v", tc.turn, got, tc.want)
		}
	}
}

// A window left idle past the quiet period was swept as ended, and what it did
// once the user came back was never recorded: the transcript was settled.
func TestASessionThatWentOnAfterItWasRecordedHasItsRecordBroughtUpToDate(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 3*time.Hour)
	onMachine(t, s)
	wrote, _, _ := Sweep(vault, "shop", now)
	if len(wrote) != 1 {
		t.Fatalf("first resume wrote %d", len(wrote))
	}

	later := *s
	later.Turns = append(append([]transcript.Turn{}, s.Turns...),
		transcript.Turn{Role: "tool", Tool: "edit_file", Input: "internal/cart/refund.go"})
	later.Ended = now.Add(-time.Hour).Unix()
	onMachine(t, &later)

	again, grew, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(again) != 0 || len(grew) != 1 {
		t.Fatalf("wrote %+v, grew %+v, problems %v; want the one record grown", again, grew, problems)
	}
	if grew[0].Slug != wrote[0].Slug {
		t.Errorf("the record moved from %s to %s, which breaks whatever links to it", wrote[0].Slug, grew[0].Slug)
	}
	all, _ := session.History(vault, "shop", 0)
	if len(all) != 1 {
		t.Fatalf("%d checkpoints on disk, want the one record", len(all))
	}
	if !strings.Contains(strings.Join(all[0].Files, " "), "refund.go") || all[0].TS != later.Ended {
		t.Errorf("the record on disk does not have the later work: %+v", all[0])
	}
	if !strings.Contains(SweepNotice(again, grew, problems), "up to date") {
		t.Errorf("growing a record was not announced: %q", SweepNotice(again, grew, problems))
	}

	if again, grew, _ := Sweep(vault, "shop", now); len(again)+len(grew) != 0 {
		t.Errorf("a record already up to date was touched again: wrote %+v, grew %+v", again, grew)
	}
}

// Records are ordered by their file's name. Rewriting one to end after a
// checkpoint written in between would leave the older of the two on top.
func TestARecordIsNotRewrittenPastACheckpointWrittenAfterIt(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 3*time.Hour)
	onMachine(t, s)
	if wrote, _, _ := Sweep(vault, "shop", now); len(wrote) != 1 {
		t.Fatalf("first resume wrote %d", len(wrote))
	}
	between := agentCheckpointAt(t, vault, now.Add(-2*time.Hour).Unix(), "the other window's handoff")

	later := *s
	later.Turns = append(append([]transcript.Turn{}, s.Turns...),
		transcript.Turn{Role: "tool", Tool: "edit_file", Input: "internal/cart/refund.go"})
	later.Ended = now.Add(-time.Hour).Unix()
	onMachine(t, &later)

	wrote, grew, problems := Sweep(vault, "shop", now)
	if len(problems) != 0 || len(grew) != 0 || len(wrote) != 1 {
		t.Fatalf("wrote %+v, grew %+v, problems %v; want a new record", wrote, grew, problems)
	}
	latest, _ := session.Latest(vault, "shop")
	if latest == nil || latest.Session != wrote[0].Session {
		t.Errorf("latest is %+v, want the new record, after %s", latest, between.Session)
	}
}

// The hook writes its record when the session closes, which after a long
// SessionEnd, or a machine that slept, is well after its transcript's last
// line. It names its session, so it is matched however late it ran.
func TestASessionTheHookRecordedIsNotSweptAgainHoweverLateTheHookRan(t *testing.T) {
	vault := t.TempDir()
	now := time.Now()
	s := endedAgo(now, 3*time.Hour)
	s.Harness = "claude-code"
	if _, err := session.WriteAuto(vault, session.Checkpoint{
		Project: "shop", Agent: "claude-code", Files: []string{"internal/cart/checkout.go"},
		State: session.ActivityLogStateFor(s.ID), TS: s.Ended + 3600,
	}); err != nil {
		t.Fatal(err)
	}
	onMachine(t, s)

	if wrote, grew, problems := Sweep(vault, "shop", now); len(wrote)+len(grew)+len(problems) != 0 {
		t.Errorf("the hook's record was not recognised: wrote %+v, grew %+v, problems %v", wrote, grew, problems)
	}
}

// liveClaudeCode makes this test process a running Claude Code on session id,
// with the process start procStart claims, beside the projects root.
func liveClaudeCode(t *testing.T, root string, pid int, id, procStart string) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(root), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"pid":` + strconv.Itoa(pid) + `,"sessionId":"` + id + `","procStart":"` + procStart + `","status":"idle"}`
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A window left idle for an hour is not a session that ended. Claude Code
// says which sessions its running processes are on, so for it the sweep does
// not have to guess.
func TestAClaudeCodeSessionStillOpenIsNotSweptHoweverLongItHasBeenIdle(t *testing.T) {
	now := time.Now()
	path := onDisk(t, now, now.Add(-3*time.Hour), now.Add(-2*time.Hour), true)
	root := filepath.Dir(filepath.Dir(path))
	liveClaudeCode(t, root, os.Getpid(), "lost-1", ourStart(t))

	vault := t.TempDir()
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 0 || len(problems) != 0 {
		t.Errorf("a session still open was recorded as ended: wrote %+v, problems %v", wrote, problems)
	}
}

// A host that was killed leaves its file behind. That is the session the sweep
// is for, not one to skip.
func TestAClaudeCodeSessionWhoseProcessHasGoneIsSwept(t *testing.T) {
	now := time.Now()
	path := onDisk(t, now, now.Add(-3*time.Hour), now.Add(-2*time.Hour), true)
	root := filepath.Dir(filepath.Dir(path))
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	liveClaudeCode(t, root, gone.Process.Pid, "lost-1", ourStart(t))

	vault := t.TempDir()
	if wrote, _, problems := Sweep(vault, "shop", now); len(wrote) != 1 || len(problems) != 0 {
		t.Errorf("a killed session's leftover file kept it from being recorded: wrote %+v, problems %v", wrote, problems)
	}
}

// ourStart is this process's start in the form Claude Code writes, where the
// platform can say; elsewhere any live pid is taken as the session's own.
func ourStart(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Skipf("ps cannot say when this process started: %v", err)
	}
	at, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
	if err != nil {
		t.Skipf("ps lstart in a form this does not read: %q", out)
	}
	return at.UTC().Format("Mon Jan _2 15:04:05 2006")
}

// The server names a project by its repository root, so a host started in
// shop/cart files its checkpoints under shop. The sweep named the transcript
// after cart, and the one session that most needed recording was the one it
// never looked at.
func TestASessionStartedInASubdirectoryOfTheRepositoryIsSwept(t *testing.T) {
	now := time.Now()
	repo := filepath.Join(t.TempDir(), "shop")
	cwd := filepath.Join(repo, "cart")
	for _, d := range []string{filepath.Join(repo, ".git"), cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "projects")
	t.Setenv(transcript.LogosClaudeProjectsEnv, root)
	t.Setenv(transcript.LogosCursorStorageEnv, t.TempDir())
	t.Setenv(transcript.LogosCodexSessionsEnv, t.TempDir())

	start, end := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	line := func(kind string, at time.Time, message string) string {
		return `{"type":"` + kind + `","sessionId":"lost-1","cwd":` + strconv.Quote(cwd) +
			`,"timestamp":"` + at.UTC().Format(time.RFC3339) + `","message":` + message + `}`
	}
	lines := []string{
		line("user", start, `{"role":"user","content":"fix the checkout crash"}`),
		line("assistant", start, `{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./cart"}}]}`),
		line("user", end, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}`),
	}
	dir := filepath.Join(root, strings.NewReplacer("/", "-", ".", "-").Replace(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lost-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, end, end); err != nil {
		t.Fatal(err)
	}

	if wrote, _, problems := Sweep(t.TempDir(), "shop", now); len(wrote) != 1 || len(problems) != 0 {
		t.Errorf("a session started in a subdirectory of shop was not recorded under shop: wrote %+v, problems %v", wrote, problems)
	}
}
