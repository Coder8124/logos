package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Coder8124/logos/internal/session"
)

// onPath stands in for the PATH: only the named binaries are installed.
func onPath(t *testing.T, bins ...string) {
	t.Helper()
	was := lookPath
	lookPath = func(bin string) (string, error) {
		if slices.Contains(bins, bin) {
			return "/fake/bin/" + bin, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = was })
}

// keys plays one scripted answer per menu, in order, and records each
// menu's title so a test can say which menus appeared.
func keys(t *testing.T, answers ...func(rows []string) (int, pick)) *[]string {
	t.Helper()
	var titles []string
	was := pickOnTerminal
	pickOnTerminal = func(title, hint string, rows []string) (int, pick, error) {
		titles = append(titles, title)
		if len(answers) == 0 {
			t.Fatalf("an unexpected menu appeared: %q", title)
		}
		a := answers[0]
		answers = answers[1:]
		i, res := a(rows)
		return i, res, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	return &titles
}

func choose(i int) func([]string) (int, pick) {
	return func([]string) (int, pick) { return i, picked }
}

func press(res pick) func([]string) (int, pick) {
	return func([]string) (int, pick) { return 0, res }
}

var twoConvs = []session.Conversation{{ID: "aaa111", Name: "first"}, {ID: "bbb222", Name: "second"}}

func TestTheAgentMenuOffersOnlyTheAgentsOnThePath(t *testing.T) {
	onPath(t, "codex", "opencode")
	var names []string
	for _, l := range availableLaunchers() {
		names = append(names, l.Name)
	}
	if !slices.Equal(names, []string{"Codex", "opencode"}) {
		t.Errorf("offered %v, want only the installed Codex and opencode", names)
	}
}

func TestPickingAnAgentForAConversationReturnsBoth(t *testing.T) {
	onPath(t, "claude", "codex")
	var agentRows []string
	keys(t, choose(2), func(rows []string) (int, pick) { agentRows = rows; return 2, picked })
	c, l, ok, err := chooseResume("p", twoConvs)
	if err != nil || !ok || c == nil || c.ID != "bbb222" || l == nil || l.Name != "Codex" {
		t.Fatalf("got conv=%v agent=%v ok=%v err=%v, want bbb222 in Codex", c, l, ok, err)
	}
	if want := []string{"print the handoff here", "start Claude Code", "start Codex"}; !slices.Equal(agentRows, want) {
		t.Errorf("agent menu rows %q, want %q", agentRows, want)
	}
}

// Enter, Enter must still print the handoff: the menu's first row is the
// command's old behaviour, so nobody who learned it is sent into an agent.
func TestEnterOnTheAgentMenuStillPrintsTheHandoff(t *testing.T) {
	onPath(t, "claude")
	keys(t, choose(1), choose(0))
	c, l, ok, err := chooseResume("p", twoConvs)
	if err != nil || !ok || c == nil || c.ID != "aaa111" || l != nil {
		t.Fatalf("got conv=%v agent=%v ok=%v err=%v, want aaa111 printed here", c, l, ok, err)
	}
}

func TestEscOnTheAgentMenuGoesBackToTheConversations(t *testing.T) {
	onPath(t, "claude")
	titles := keys(t, choose(1), press(wentBack), choose(2), choose(1))
	c, l, ok, err := chooseResume("p", twoConvs)
	if err != nil || !ok || c == nil || c.ID != "bbb222" || l == nil {
		t.Fatalf("got conv=%v agent=%v ok=%v err=%v, want bbb222 in Claude Code after going back", c, l, ok, err)
	}
	if len(*titles) != 4 || !strings.HasPrefix((*titles)[2], "Resume which conversation") {
		t.Errorf("menus shown %q, want the conversations again after Esc", *titles)
	}
}

func TestQOnTheAgentMenuCancelsEverything(t *testing.T) {
	onPath(t, "claude")
	keys(t, choose(1), press(cancelled))
	if c, l, ok, err := chooseResume("p", twoConvs); ok || c != nil || l != nil || err != nil {
		t.Errorf("got conv=%v agent=%v ok=%v err=%v, want a cancel", c, l, ok, err)
	}
}

// The whole project is the pack, not a conversation an agent can be pointed
// at by id, and with nothing installed the menu would hold one row.
func TestNoAgentMenuForTheWholeProjectOrWithNoAgentInstalled(t *testing.T) {
	onPath(t, "claude")
	keys(t, choose(0))
	if c, l, ok, _ := chooseResume("p", twoConvs); !ok || c != nil || l != nil {
		t.Errorf("whole project: got conv=%v agent=%v ok=%v", c, l, ok)
	}
	onPath(t)
	keys(t, choose(1))
	if c, l, ok, _ := chooseResume("p", twoConvs); !ok || c == nil || l != nil {
		t.Errorf("no agents: got conv=%v agent=%v ok=%v", c, l, ok)
	}
}

// Each CLI reads its first message in a different place, and the wrong one
// either runs once and exits (claude -p, codex exec) or is taken as a path.
func TestEachAgentGetsThePromptWhereItStaysInteractive(t *testing.T) {
	want := map[string][]string{
		"claude":       {"P"},
		"codex":        {"P"},
		"cursor-agent": {"P"},
		"gemini":       {"-i", "P"},
		"opencode":     {"--prompt", "P"},
	}
	for _, l := range launchers {
		if got := l.args("P"); !slices.Equal(got, want[l.Bin]) {
			t.Errorf("%s args %q, want %q", l.Name, got, want[l.Bin])
		}
	}
	if len(launchers) != len(want) {
		t.Errorf("%d launchers, %d expectations — a new agent needs its args checked here", len(launchers), len(want))
	}
}

func TestALaunchedAgentIsToldToCallTheResumeToolForThatConversation(t *testing.T) {
	onPath(t, "claude")
	var ran *exec.Cmd
	was := runAgent
	runAgent = func(cmd *exec.Cmd) error { ran = cmd; return nil }
	t.Cleanup(func() { runAgent = was })

	l := availableLaunchers()[0]
	if err := launchAgent(l, "proj", "conv-42"); err != nil {
		t.Fatal(err)
	}
	if ran == nil {
		t.Fatal("the agent was never started")
	}
	wd, _ := os.Getwd()
	if ran.Path != "/fake/bin/claude" || ran.Dir != wd || ran.Stdin != os.Stdin || ran.Stdout != os.Stdout {
		t.Errorf("started %s in %s with stdin=%v stdout=%v, want the resolved claude here on this terminal", ran.Path, ran.Dir, ran.Stdin, ran.Stdout)
	}
	prompt := ran.Args[len(ran.Args)-1]
	for _, s := range []string{"logos resume tool", `project "proj"`, `conversation "conv-42"`, "logos resume proj conv-42"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt does not say %q:\n%s", s, prompt)
		}
	}
}

// An agent that fails must not leave logos exiting 0, as if the session ran.
func TestAnAgentThatExitsNonZeroIsReported(t *testing.T) {
	onPath(t, "codex")
	was := runAgent
	runAgent = func(*exec.Cmd) error { return exec.Command("sh", "-c", "exit 3").Run() }
	t.Cleanup(func() { runAgent = was })
	err := launchAgent(availableLaunchers()[0], "p", "c")
	if err == nil || !strings.Contains(err.Error(), "Codex exited with status 3") {
		t.Errorf("got %v, want Codex's exit status named", err)
	}

	runAgent = func(*exec.Cmd) error { return errors.New("permission denied") }
	if err := launchAgent(availableLaunchers()[0], "p", "c"); err == nil || !strings.Contains(err.Error(), "could not start Codex") {
		t.Errorf("got %v, want the start failure named", err)
	}
}
