package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// `logos hook <host> <event>` is the session-start hook for the hosts that have
// no Logos plugin. Claude Code gets the plugin, whose bash hook does this and
// more; Cursor and Codex load a user-level hooks file instead, and each names
// the field it injects context through differently, so the shape of the answer
// is the only thing that differs between them.
//
// The rules are the plugin hook's rules, because a hook that misbehaves poisons
// every session in the host: never fail (always exit 0), never stall
// (everything bounded), and say nothing at all when there is nothing to say —
// a host that gets malformed output from a hook may disable it.
func hookCmd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: logos hook <cursor|codex> session-start")
	}
	host, event := args[0], args[1]
	if _, known := hookShapes[host]; !known {
		return fmt.Errorf("logos hook: unknown host %q — known: cursor, codex", host)
	}
	if event != "session-start" {
		// An unknown event is not an error: a host that grows one and calls us
		// must not start reporting failures at the user.
		return nil
	}
	self, err := selfPath()
	if err != nil {
		return nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	if out := hookOutput(host, handoffFrom(self, dir)); out != "" {
		fmt.Println(out)
	}
	return nil
}

// hookShapes is how each host wants injected context handed to it. They agree
// on everything except the name of the field, which is the whole reason this
// command takes a host at all.
var hookShapes = map[string]func(string) any{
	"cursor": func(text string) any {
		return map[string]string{"additional_context": text}
	},
	"codex": func(text string) any {
		return map[string]any{"hookSpecificOutput": map[string]string{"additionalContext": text}}
	},
}

// hookOutput is the line to print, or "" when there is nothing to inject.
func hookOutput(host, handoff string) string {
	shape, ok := hookShapes[host]
	if !ok || handoff == "" {
		return ""
	}
	raw, err := json.Marshal(shape(handoff))
	if err != nil {
		return ""
	}
	return string(raw)
}

// handoffFrom returns the context pack to inject for the project dir belongs
// to, or "" when there is nothing worth injecting.
//
// It runs logos rather than calling resume's code directly, the way the
// plugin's bash hook does: the hook has to answer within the host's timeout or
// be killed, and a separate process is one the parent can kill.
func handoffFrom(self, dir string) string {
	project := projectFor(dir)
	if project == "" {
		return ""
	}
	cmd := exec.Command(self, "resume", project)
	cmd.Dir = dir
	// LOGOS_EMBED=off: the handoff is markdown and needs no vectors, but resume
	// embeds its query on the way, and a model runtime that is slow to load has
	// taken this past a host's hook timeout before.
	cmd.Env = append(os.Environ(), "LOGOS_EMBED=off")
	out, err := runBounded(cmd, 8*time.Second)
	if err != nil {
		return ""
	}
	// resume on a project with no checkpoint still returns standing memories
	// and notes. Useful to a person, noise to a model that has just been handed
	// the repository — so only an actual handoff is injected, and the heading is
	// written only alongside a checkpoint.
	text := string(out)
	if !strings.HasPrefix(text, "## Where we left off") && !strings.Contains(text, "\n## Where we left off") {
		return ""
	}
	return text
}

// runBounded runs cmd and kills it if it outlives d, because a hook that hangs
// hangs the session it was supposed to help.
func runBounded(cmd *exec.Cmd, d time.Duration) ([]byte, error) {
	var out []byte
	var err error
	done := make(chan struct{})
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(d):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done
		return nil, fmt.Errorf("resume did not answer in %s", d)
	}
}
