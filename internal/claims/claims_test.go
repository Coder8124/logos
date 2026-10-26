package claims

import (
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/activity"
)

// logged starts a recording vault holding events, each stamped now unless it
// says otherwise.
func logged(t *testing.T, events ...activity.Event) string {
	t.Helper()
	vault := t.TempDir()
	if err := activity.SetRecording(vault, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, e := range events {
		if e.TS == 0 {
			e.TS = now
		}
		if e.Kind == "" {
			e.Kind = activity.KindTool
		}
		if err := activity.Append(vault, e); err != nil {
			t.Fatal(err)
		}
	}
	return vault
}

func bash(project, session, command string) activity.Event {
	return activity.Event{Project: project, Session: session, Tool: "Bash", Summary: "Bash: " + command}
}

func check(t *testing.T, vault string, verified ...string) Report {
	t.Helper()
	r, err := Check(vault, "p", verified)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The log is read newest first and keeps file order within a second, so the
// first event of the newest second is the session that wrote earlier in it.
// Checking that one would hold a checkpoint up against somebody else's commands.
func TestTheSessionCheckedIsTheOneThatWroteLastInTheSameSecond(t *testing.T) {
	vault := logged(t, bash("p", "other", "go test ./..."), bash("p", "mine", "go build ./..."))
	r := check(t, vault, "go test ./... passes")
	if r.Session != "mine" || len(r.Unseen) != 1 {
		t.Errorf("checked session %q, unseen %q; want mine, with the test it never ran marked", r.Session, r.Unseen)
	}
}

// With recording switched off the log stops at the day it was turned off, and
// its newest session is a past one: nothing it lacks says anything about now.
func TestWithRecordingOffNothingIsChecked(t *testing.T) {
	vault := logged(t, bash("p", "s", "go build ./..."), bash("q", "o", "go test ./..."))
	if err := activity.SetRecording(vault, false); err != nil {
		t.Fatal(err)
	}
	if r := check(t, vault, "go test ./... passes"); r.Session != "" || len(r.Unseen) != 0 {
		t.Errorf("checked a log that is not being kept: %+v", r)
	}
}

// A host with no hooks logs nothing, so the newest session in the log is
// another one, hours old. Its commands are not this checkpoint's.
func TestASessionThatLoggedNothingRecentlyIsNotChecked(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour).Unix()
	e := bash("p", "yesterday", "go build ./...")
	e.TS = old
	f := bash("p", "yesterday", "go test ./x")
	f.TS, f.Failed = old, true
	vault := logged(t, e, f, bash("q", "o", "go test ./..."))
	if r := check(t, vault, "go test ./auth passes"); r.Session != "" || len(r.Unseen) != 0 || len(r.Failures) != 0 {
		t.Errorf("a stale session was held against this checkpoint: %+v", r)
	}
}

// "go test passes" from a session whose only go test failed is the claim this
// exists to catch; a failed run is not support for it.
func TestARunThatFailedDoesNotSupportAVerifiedLine(t *testing.T) {
	f := bash("p", "s", "go test ./cart")
	f.Failed = true
	vault := logged(t, f)
	r := check(t, vault, "go test ./cart passes")
	if len(r.Unseen) != 1 || len(r.Failures) != 1 {
		t.Errorf("a failed run supported the line: %+v", r)
	}
}

// The summary is cut at 160 runes; the hook keeps the commands apart from the
// cut, so a go vet at the end of a long line still counts as run.
func TestACommandPastTheSummaryCutStillCountsAsRun(t *testing.T) {
	line := "cd /Users/someone/IdeaProjects/a-very-long-checkout-name-for-this && LOGOS_VAULT=/tmp/scratch-vault go build ./... && go test ./... && go vet ./... && gofmt -l ."
	e, err := activity.FromHook("PostToolUse", []byte(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"`+line+`"}}`), "p")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(e.Summary)) < 160 {
		t.Fatalf("the line was not long enough to be cut: %q", e.Summary)
	}
	vault := logged(t, e)
	if r := check(t, vault, "go vet ./... clean", "gofmt clean"); len(r.Unseen) != 0 {
		t.Errorf("commands past the cut were marked unverified: %q", r.Unseen)
	}
}

// An event logged before the hook kept commands apart has only its cut
// summary, and what ran past the cut is unknown; no line is marked on it.
func TestACutSummaryWithNoCommandsKeptMarksNothing(t *testing.T) {
	vault := logged(t, bash("p", "s", "cd /x && go build ./... && go…"), bash("q", "o", "go test ./..."))
	if r := check(t, vault, "go test ./... passes"); len(r.Unseen) != 0 {
		t.Errorf("marked a line the cut may have hidden: %q", r.Unseen)
	}
}

// A subcommand only counts once the log has seen it, and a project that never
// ran go test before would let "go test passes" through on any go run. Other
// projects in the vault teach the log what go's subcommands are.
func TestSubcommandsAreLearnedFromTheWholeVault(t *testing.T) {
	vault := logged(t, bash("elsewhere", "o", "go test ./..."), bash("p", "s", "go build ./..."))
	if r := check(t, vault, "go test ./auth passes"); len(r.Unseen) != 1 {
		t.Errorf("go test was not read as a command: %+v", r)
	}
}

// A Read of a missing file or an Edit that missed its string fails in nearly
// every session and rules nothing out; only failing shell commands are asked
// about.
func TestOnlyFailingShellCommandsAreAskedAbout(t *testing.T) {
	r1 := activity.Event{Project: "p", Session: "s", Tool: "Read", Summary: "Read gone.go", Failed: true}
	r2 := activity.Event{Project: "p", Session: "s", Tool: "Edit", Summary: "Edit a.go", Failed: true}
	vault := logged(t, r1, r2, bash("p", "s", "go build ./..."))
	if r := check(t, vault); len(r.Failures) != 0 {
		t.Errorf("tool misses were counted as failures: %q", r.Failures)
	}
}

// A procedure's verify is evidence for it only when it ran after the
// procedure was shown, in the same session, and passed: an earlier run, a
// failing one, or one in another session says nothing about whether
// following the procedure worked.
func TestAVerifyIsPassedOnlyByARunAfterTheProcedureWasShown(t *testing.T) {
	shown := int64(1000)
	run := func(session, command string, ts int64, failed bool) activity.Event {
		e := bash("p", session, command)
		e.Kind, e.TS, e.Failed = activity.KindTool, ts, failed
		return e
	}
	cases := []struct {
		name   string
		events []activity.Event
		want   bool
	}{
		{"passed after", []activity.Event{run("s", "make migrate-check", shown+5, false)}, true},
		{"passed before", []activity.Event{run("s", "make migrate-check", shown-5, false)}, false},
		{"failed after", []activity.Event{run("s", "make migrate-check", shown+5, true)}, false},
		{"another session", []activity.Event{run("t", "make migrate-check", shown+5, false)}, false},
		{"other subcommand", []activity.Event{run("s", "make build", shown+5, false)}, false},
	}
	for _, c := range cases {
		if got := Passed("cd db && make migrate-check", c.events, "s", shown); got != c.want {
			t.Errorf("%s: Passed = %v, want %v", c.name, got, c.want)
		}
	}
	both := []activity.Event{run("s", "go vet ./...", shown+1, false), run("s", "go test ./...", shown+2, false)}
	if !Passed("go vet ./... && go test ./...", both, "s", shown) {
		t.Error("a verify whose every command passed was not passed")
	}
	if Passed("go vet ./... && go test ./...", both[:1], "s", shown) {
		t.Error("a verify with a command never run was passed")
	}
	if Passed("cd db", both, "s", shown) {
		t.Error("a verify that is only a cd was passed")
	}
	if !Passed("`make migrate-check`", []activity.Event{run("s", "make migrate-check", shown+5, false)}, "s", shown) {
		t.Error("a verify written in backticks was not passed by its run")
	}
	passThenFail := []activity.Event{run("s", "make migrate-check", shown+5, false), run("s", "make migrate-check", shown+9, true)}
	if Passed("make migrate-check", passThenFail, "s", shown) {
		t.Error("a verify whose last run failed was passed by an earlier one")
	}
	for _, line := range []string{"make migrate-check | tail -5", "make migrate-check || true"} {
		if Passed("make migrate-check", []activity.Event{run("s", line, shown+5, false)}, "s", shown) {
			t.Errorf("a verify run as %q, whose status is not its own, was passed", line)
		}
	}
}

func TestAPipePastTheSummaryCutStillKeepsAVerifyFromPassing(t *testing.T) {
	line := "cd " + strings.Repeat("x", 170) + " && make migrate-check | tail -5"
	e, err := activity.FromHook("PostToolUse", []byte(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"`+line+`"}}`), "p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Summary, "|") {
		t.Fatalf("the summary kept the pipe, so this does not test the cut: %q", e.Summary)
	}
	e.TS = 1005
	if Passed("make migrate-check", []activity.Event{e}, "s", 1000) {
		t.Error("a verify piped past the summary's cut was passed")
	}
}
