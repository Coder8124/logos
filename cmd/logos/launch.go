package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// launcher is an agent CLI the resume picker can start on a conversation:
// its own interactive session, opened with a first prompt.
type launcher struct {
	Name string
	Bin  string
	// args puts the prompt where this CLI reads a first message and stays
	// interactive. Each one differs, and the wrong flag (claude -p, codex
	// exec) runs the prompt once and exits, which looks like a crash.
	args func(prompt string) []string
	path string // resolved by availableLaunchers
}

// launchers are the agents setup wires that have a terminal CLI. Claude
// Desktop and the IDEs have no command that opens a session with a prompt,
// so they are not offered rather than offered and failing.
var launchers = []launcher{
	{Name: "Claude Code", Bin: "claude", args: func(p string) []string { return []string{p} }},
	{Name: "Codex", Bin: "codex", args: func(p string) []string { return []string{p} }},
	{Name: "Cursor", Bin: "cursor-agent", args: func(p string) []string { return []string{p} }},
	{Name: "Gemini CLI", Bin: "gemini", args: func(p string) []string { return []string{"-i", p} }},
	{Name: "opencode", Bin: "opencode", args: func(p string) []string { return []string{"--prompt", p} }},
}

// lookPath and runAgent are vars so a test can stand in for the PATH and the
// child process without installing five agents.
var (
	lookPath = exec.LookPath
	runAgent = func(cmd *exec.Cmd) error { return cmd.Run() }
)

// availableLaunchers is the agents on PATH, in the order above. One that is
// not installed is left out of the menu: a row that can only fail is worse
// than a shorter menu.
func availableLaunchers() []launcher {
	var out []launcher
	for _, l := range launchers {
		if p, err := lookPath(l.Bin); err == nil {
			l.path = p
			out = append(out, l)
		}
	}
	return out
}

// launchPrompt names the tool and its arguments. A prompt that only says
// "resume conversation X" was answered from nothing by Codex, which called no
// tool and replied as if it had; naming the call is what made it happen. The
// shell fallback is for an agent this vault was never wired into.
//
// The checkpoint itself is not pasted in. It was written by another agent,
// and in the first prompt it would arrive as the user's own instruction;
// through the tool it arrives marked as a record of earlier work.
func launchPrompt(project, id string) string {
	return fmt.Sprintf("Call the logos resume tool with project %q and conversation %q, then continue from that conversation's next step. "+
		"If you have no logos tools, run `logos resume %s %s` in the shell and read its output instead.", project, id, project, id)
}

// launchAgent starts the agent on a conversation in this terminal and waits
// for it, so quitting the agent lands the person back at their shell.
func launchAgent(l launcher, project, id string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("could not start %s: %w", l.Name, err)
	}
	fmt.Printf("Starting %s in %s on conversation %s of %s.\n", l.Name, redactHome(dir), id, project)
	if dirFor(project) == "" {
		// The agent's own idea of the project comes from its folder; the
		// prompt names the project, so the resume still finds it, but the
		// agent's edits and checkpoints land wherever it was started.
		fmt.Printf("This folder is not %s, so the agent works here — cd into the project first to work there.\n", project)
	}
	cmd := exec.Command(l.path, l.args(launchPrompt(project, id))...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl-C reaches every process in the foreground group, logos too. The
	// agent handles it (Claude Code asks twice before quitting); logos must
	// not die under it and hand the shell back while the agent still owns
	// the terminal. Notify, not Ignore: an ignored signal stays ignored in
	// the child, and the agent would lose its Ctrl-C.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	err = runAgent(cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("%s exited with status %d", l.Name, exit.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("could not start %s: %w", l.Name, err)
	}
	return nil
}
