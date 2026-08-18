package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pragun/brain/internal/contextpack"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
)

// The continuity commands, mirroring the MCP tools of the same names.
//
// They exist so the handoff can be driven and inspected without an MCP host in
// the loop. A tool that can only be exercised through Claude Desktop is a tool
// whose failures are someone else's bug report.

// runNote appends a working note: cheap, uncommitted, the working tree.
func runNote(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: brain note <project> <what you did>")
	}
	project, text := args[0], strings.Join(args[1:], " ")

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		return err
	}
	if _, err := session.AddNote(ix.DB, project, agentName(), text); err != nil {
		return err
	}
	fmt.Println("noted — uncommitted until you checkpoint.")
	return nil
}

// runCheckpoint commits the session to the vault. Sections come from flags, or
// from stdin as a markdown document when piped — an agent writing a checkpoint
// has prose, not a shell-quoting problem.
func runCheckpoint(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain checkpoint <project> [--task ...] [--next ...] " +
			"[--decided ...] [--failed ...] [--question ...] [--file ...] [--handoff <agent>]\n" +
			"       ...or pipe a markdown checkpoint on stdin")
	}
	c := &session.Checkpoint{Project: args[0], Agent: agentName()}

	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		val := func() string {
			if i+1 < len(rest) {
				i++
				return rest[i]
			}
			return ""
		}
		switch rest[i] {
		case "--task":
			c.Task = val()
		case "--state":
			c.State = val()
		case "--next":
			c.Next = val()
		case "--decided":
			c.Decisions = append(c.Decisions, val())
		case "--failed":
			c.Failed = append(c.Failed, val())
		case "--question":
			c.Questions = append(c.Questions, val())
		case "--file":
			c.Files = append(c.Files, val())
		case "--handoff", "--to":
			c.HandoffTo = val()
		case "--agent":
			c.Agent = val()
		default:
			return fmt.Errorf("unknown flag %q", rest[i])
		}
	}

	// Piped input fills whatever the flags left blank, so the two ways of
	// writing a checkpoint compose instead of competing.
	if stdinIsPiped() {
		raw, err := readAll(os.Stdin)
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) != "" {
			merge(c, session.ParseCheckpoint(raw))
		}
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		return err
	}
	if err := session.Commit(ix.DB, ix.Vault, c); err != nil {
		return err
	}
	fmt.Printf("checkpoint written: %s.md\n", c.Slug)
	fmt.Println("run `brain index` to make it searchable.")
	return nil
}

// runResume prints where the last agent stopped, followed by full context.
func runResume(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain resume <project> [--budget <tokens>]")
	}
	project := args[0]
	budget := 0
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--budget" || args[i] == "-b" {
			budget, _ = strconv.Atoi(args[i+1])
		}
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			return err
		}
	}

	rt, err := openRouter()
	if err != nil {
		return err
	}
	embedModel, _ := rt.Model(router.T0)

	pack, err := contextpack.Build(ix, rt.Local(), embedModel, contextpack.Request{
		Task: "resume work on " + project, Hint: project, Budget: budget,
	})
	if err != nil {
		return err
	}
	fmt.Print(pack.Render())
	if pack.Checkpoint == nil {
		fmt.Println("\n(no checkpoint yet for this project — this is context, not a handoff)")
	}
	return nil
}

// runSessionLog shows the checkpoint history for a project: the commit log.
func runSessionLog(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain sessions <project>")
	}
	project := args[0]

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		return err
	}

	hist, err := session.History(ix.Vault, project, 20)
	if err != nil {
		return err
	}
	if len(hist) == 0 {
		fmt.Printf("no checkpoints for %s yet.\n", project)
	}
	for _, c := range hist {
		who := c.Agent
		if who == "" {
			who = "agent"
		}
		fmt.Printf("%s  %s\n", c.Session, who)
		if c.Task != "" {
			fmt.Printf("    %s\n", oneLineOf(c.Task))
		}
		if c.Next != "" {
			fmt.Printf("    next: %s\n", oneLineOf(c.Next))
		}
		if c.HandoffTo != "" {
			fmt.Printf("    handed off to %s\n", c.HandoffTo)
		}
	}

	if notes, err := session.Uncommitted(ix.DB, project); err == nil && len(notes) > 0 {
		fmt.Printf("\nuncommitted (%d):\n", len(notes))
		for _, n := range notes {
			fmt.Printf("    %s\n", oneLineOf(n.Text))
		}
	}
	return nil
}

// merge fills empty fields of dst from src. Flags win over piped input because
// the flag was typed more recently and more deliberately.
func merge(dst *session.Checkpoint, src session.Checkpoint) {
	if dst.Task == "" {
		dst.Task = src.Task
	}
	if dst.State == "" {
		dst.State = src.State
	}
	if dst.Next == "" {
		dst.Next = src.Next
	}
	if len(dst.Decisions) == 0 {
		dst.Decisions = src.Decisions
	}
	if len(dst.Failed) == 0 {
		dst.Failed = src.Failed
	}
	if len(dst.Questions) == 0 {
		dst.Questions = src.Questions
	}
	if len(dst.Files) == 0 {
		dst.Files = src.Files
	}
	if dst.HandoffTo == "" {
		dst.HandoffTo = src.HandoffTo
	}
}

// agentName identifies who is checkpointing. BRAIN_AGENT lets a wrapper script
// or an MCP host say which tool it is, so a handoff names a real counterpart
// rather than "agent".
func agentName() string {
	if a := strings.TrimSpace(os.Getenv("BRAIN_AGENT")); a != "" {
		return a
	}
	return "cli"
}

func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice == 0
}

func readAll(f *os.File) (string, error) {
	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String(), sc.Err()
}

func oneLineOf(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		s = s[:89] + "…"
	}
	return s
}
