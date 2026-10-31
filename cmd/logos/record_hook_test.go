package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
)

// recordHookBudget is what one run of plugin/hooks/record.sh may cost.
//
// The ceiling it answers to is the 5 s timeout hooks.json gives every
// record.sh entry, and past that line Claude Code kills the hook and the event
// is gone with nothing on screen to say so. This test measures the quietest
// version of the path: one candidate binary for the resolver to probe, an idle
// disk, a warm page cache. A real machine probes every logos on PATH and in the
// install directories (an npm shim is a node start on its own), and the hook
// fires while the agent's own tool call is still loading the machine. One fifth
// of the kill line is the margin that leaves for those; a hook measured at a
// second here is one that can cross five out there.
//
// Measured when this was written, on an M-series Mac with the log below:
// PostToolUse 46–47 ms, Stop 76–79 ms. Stop is the slow one because
// --ask-checkpoint decodes the whole log to find the session's work; it grows
// by roughly 3.5 ms per thousand lines, about 440 ms at 100,000.
const recordHookBudget = time.Second

// recordHookLogLines is a heavy month: a long day of agent work is a few
// thousand tool calls, and retention keeps up to two months of files.
const recordHookLogLines = 10_000

// record.sh runs on every tool call, and a timeout loses the event without
// anyone seeing it. The one part of it that grows is the read of the activity
// log the Stop hook does to decide whether to ask for a checkpoint, so this
// drives the real hook, the real resolver and the real binary against a log of
// that size, and holds each event's median over several runs under the budget.
//
// A fast hook that recorded nothing would pass a timing check, so the test also
// requires every run to have landed in the log and the first Stop to have
// asked: the time measured is the time of the work, not of an early exit.
func TestRecordHookStaysUnderBudgetOnALargeActivityLog(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary and a 10,000-line activity log")
	}
	if raceEnabled {
		t.Skip("a wall-clock budget means nothing under the race detector; the macOS CI job runs this")
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "logos"), ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building logos: %v\n%s", err, out)
	}
	// macOS checks a binary it has never run before on its first exec, which
	// cost ~400 ms here. That is paid once per install, not per tool call, so
	// it is paid before the clock starts.
	if out, err := exec.Command(filepath.Join(bin, "logos"), "--version").CombinedOutput(); err != nil {
		t.Fatalf("the built logos does not run: %v\n%s", err, out)
	}
	hook, err := filepath.Abs("../../plugin/hooks/record.sh")
	if err != nil {
		t.Fatal(err)
	}

	vault := t.TempDir()
	dir := filepath.Join(vault, activity.Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "budget")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}

	// Written straight to the month's file: ten thousand Appends would each
	// stat, open and close it, and the seeding is not what is being measured.
	now := time.Now()
	logFile := filepath.Join(dir, now.Format("2006-01")+".jsonl")
	f, err := os.Create(logFile)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	tools := []string{"Edit", "Bash", "Read"}
	for i := range recordHookLogLines {
		// Every session but the last is someone else's; the last is the one the
		// Stop below belongs to, with a Bash call in it, so the hook has work
		// to find and has to read to the end of the log to find it.
		sess := fmt.Sprintf("s%04d", i/200)
		if i >= recordHookLogLines-50 {
			sess = "live"
		}
		line, _ := json.Marshal(activity.Event{
			TS:      now.Unix() - int64(recordHookLogLines-i),
			Kind:    activity.KindTool,
			Project: "budget",
			Session: sess,
			Agent:   "claude-code",
			Tool:    tools[i%len(tools)],
			Summary: fmt.Sprintf("Bash: go test ./internal/activity/... -run TestSomething -count=1 # %d", i),
			Extra:   map[string]any{"session_id": sess + "-0000-4000-8000-000000000000"},
		})
		w.Write(append(line, '\n'))
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	payload := `{"session_id":"live-0000-4000-8000-000000000000","cwd":` + strconv.Quote(project) +
		`,"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`
	// One HOME for every run, as a user has: a fresh one each time timed the
	// first run's cost — nothing cached, nothing created yet — ten times over.
	home := t.TempDir()
	run := func(event string) (string, time.Duration) {
		cmd := exec.Command("/bin/bash", hook, event)
		cmd.Env = []string{
			"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "GOBIN=", "GOPATH=",
			noFixedInstallDirs, "LOGOS_VAULT=" + vault, "CLAUDE_PROJECT_DIR=" + project,
		}
		cmd.Stdin = strings.NewReader(payload)
		start := time.Now()
		out, err := cmd.Output()
		took := time.Since(start)
		if err != nil {
			t.Fatalf("record.sh %s: %v\n%s", event, err, out)
		}
		return string(out), took
	}

	// The budget is held against each event's median, not its slowest run:
	// one run that lost the CPU to a parallel package on a shared machine says
	// nothing about the hook, and a regression that matters moves the middle.
	const runs = 7
	took := map[string][]time.Duration{}
	var firstStop string
	for i := range runs {
		for _, event := range []string{"PostToolUse", "Stop"} {
			out, d := run(event)
			if event == "Stop" && i == 0 {
				firstStop = out
			}
			took[event] = append(took[event], d)
		}
	}

	if !strings.Contains(firstStop, `"decision":"block"`) {
		t.Fatalf("the first Stop did not ask for a checkpoint, so it never read the log it was timed reading:\n%q", firstStop)
	}
	all, err := activity.Read(vault, activity.Query{Project: "budget"})
	if err != nil {
		t.Fatal(err)
	}
	// Every run's event, plus the one marker the first Stop wrote before asking.
	if want := recordHookLogLines + 2*runs + 1; len(all) != want {
		t.Fatalf("the log holds %d events after %d runs, want %d: a run that recorded nothing was timed", len(all), 2*runs, want)
	}
	for _, event := range []string{"PostToolUse", "Stop"} {
		d := took[event]
		slices.Sort(d)
		median := d[len(d)/2]
		t.Logf("%s on a %d-line log over %d runs: median %s, slowest %s", event, recordHookLogLines, runs, median, d[len(d)-1])
		if median > recordHookBudget {
			t.Errorf("record.sh %s took a median %s on a %d-line activity log; the budget is %s against a 5 s timeout that drops the event",
				event, median, recordHookLogLines, recordHookBudget)
		}
	}
}
