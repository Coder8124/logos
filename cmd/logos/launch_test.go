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
	was := findAgent
	findAgent = func(bin string) string {
		if slices.Contains(bins, bin) {
			return "/fake/bin/" + bin
		}
		return ""
	}
	t.Cleanup(func() { findAgent = was })
}

// keys plays one scripted answer per menu, in order, and records each
// menu's title so a test can say which menus appeared.
func keys(t *testing.T, answers ...func(rows []string) (int, pick)) *[]menu {
	t.Helper()
	var shown []menu
	was := pickOnTerminal
	pickOnTerminal = func(m menu) (int, pick, int, error) {
		shown = append(shown, m)
		if len(answers) == 0 {
			t.Fatalf("an unexpected menu appeared: %q", m.title)
		}
		a := answers[0]
		answers = answers[1:]
		i, res := a(m.rows)
		return i, res, len(m.rows) + 1, nil
	}
	t.Cleanup(func() { pickOnTerminal = was })
	return &shown
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
	shown := keys(t, choose(1), press(wentBack), choose(2), choose(1))
	c, l, ok, err := chooseResume("p", twoConvs)
	if err != nil || !ok || c == nil || c.ID != "bbb222" || l == nil {
		t.Fatalf("got conv=%v agent=%v ok=%v err=%v, want bbb222 in Claude Code after going back", c, l, ok, err)
	}
	if len(*shown) != 4 || !strings.HasPrefix((*shown)[2].title, "Resume which conversation") {
		t.Fatalf("menus shown %v, want the conversations again after Esc", *shown)
	}
	// Back on the conversation that was chosen, drawn over both frames:
	// the conversations' title and three rows, the agent menu's title and two.
	if again := (*shown)[2]; again.sel != 1 || again.over != 7 {
		t.Errorf("the conversations came back with sel=%d over=%d, want sel=1 over=7", again.sel, again.over)
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
	if err := launchAgent(l, "/the/vault", "proj", "conv-42"); err != nil {
		t.Fatal(err)
	}
	if ran == nil {
		t.Fatal("the agent was never started")
	}
	wd, _ := os.Getwd()
	if ran.Path != "/fake/bin/claude" || ran.Dir != wd || ran.Stdin != os.Stdin || ran.Stdout != os.Stdout {
		t.Errorf("started %s in %s with stdin=%v stdout=%v, want the resolved claude here on this terminal", ran.Path, ran.Dir, ran.Stdin, ran.Stdout)
	}
	if !slices.Contains(ran.Env, "LOGOS_VAULT=/the/vault") {
		t.Error("the agent was not given the vault the conversation was read from")
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
	err := launchAgent(availableLaunchers()[0], "/v", "p", "c")
	if err == nil || !strings.Contains(err.Error(), "Codex exited with status 3") {
		t.Errorf("got %v, want Codex's exit status named", err)
	}

	runAgent = func(*exec.Cmd) error { return errors.New("permission denied") }
	if err := launchAgent(availableLaunchers()[0], "/v", "p", "c"); err == nil || !strings.Contains(err.Error(), "could not start Codex") {
		t.Errorf("got %v, want the start failure named", err)
	}
}

// Killed rather than exited: there is no status, and "status -1" named
// nothing that happened.
func TestAnAgentStoppedByASignalSaysSo(t *testing.T) {
	onPath(t, "codex")
	was := runAgent
	runAgent = func(*exec.Cmd) error { return exec.Command("sh", "-c", "kill -TERM $$").Run() }
	t.Cleanup(func() { runAgent = was })
	err := launchAgent(availableLaunchers()[0], "/v", "p", "c")
	if err == nil || !strings.Contains(err.Error(), "Codex was stopped") || strings.Contains(err.Error(), "-1") {
		t.Errorf("got %v, want the stop named without a made-up status", err)
	}
}

// The fallback is pasted into a shell by the agent; a name with a space or a
// quote must stay one argument.
func TestTheShellFallbackQuotesWhatTheShellWouldSplit(t *testing.T) {
	p := launchPrompt("my proj", "it's")
	if !strings.Contains(p, `logos resume 'my proj' 'it'\''s'`) {
		t.Errorf("fallback not quoted:\n%s", p)
	}
	if p := launchPrompt("brain", "20261031-a1"); !strings.Contains(p, "logos resume brain 20261031-a1`") {
		t.Errorf("a plain name was quoted:\n%s", p)
	}
}

// A worktree's folder is named for its branch; it is still the project's
// checkout, and saying otherwise sent people to cd somewhere they already were.
func TestAWorktreeOfTheProjectIsItsCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("LOGOS_PROJECT", "")
	root := t.TempDir()
	main, wt := root+"/kestrel", root+"/kestrel-feature"
	for _, args := range [][]string{
		{"init", "-q", main},
		{"-C", main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"},
		{"-C", main, "worktree", "add", "-q", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if !isProjectCheckout(wt, "kestrel") {
		t.Error("a worktree of kestrel was called not kestrel")
	}
	if isProjectCheckout(wt, "other") || isProjectCheckout(root, "kestrel") {
		t.Error("a folder of some other project was called kestrel")
	}
}
