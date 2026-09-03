package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/activity"
)

// `brain activity` — what the agents actually did, as opposed to what they
// remembered to write down.
//
// See internal/activity for why this is a JSONL file and not a table.
func runActivity(args []string) error {
	if len(args) > 0 && args[0] == "record" {
		return recordActivity(args[1:])
	}
	vault := vaultPath()

	q := activity.Query{
		Project: strings.TrimSpace(flagStr(args, "--project", "")),
		Kind:    strings.TrimSpace(flagStr(args, "--kind", "")),
		Tool:    strings.TrimSpace(flagStr(args, "--tool", "")),
		Limit:   flagInt(args, "--n", 40),
	}
	if d := flagInt(args, "--days", 0); d > 0 {
		q.Since = time.Now().AddDate(0, 0, -d)
	}

	if hasFlag(args, "--projects") {
		return listActivityProjects(vault)
	}

	events, err := activity.Read(vault, q)
	if err != nil {
		return err
	}
	// --json is the whole pitch made good: the log is yours, and this is the
	// pipe into jq for anyone who would rather not use our formatting at all.
	if hasFlag(args, "--json") {
		enc := json.NewEncoder(os.Stdout)
		for _, e := range events {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	}
	if len(events) == 0 {
		return emptyActivity(vault, q)
	}

	fmt.Printf("activity%s · %d %s\n\n", scopeLabel(q), len(events), plural(len(events), "event"))
	for _, e := range events {
		when := time.Unix(e.TS, 0).Format("Jan 02 15:04")
		where := ""
		if q.Project == "" && e.Project != "" {
			where = " [" + e.Project + "]"
		}
		sess := ""
		if e.Session != "" {
			sess = " · " + e.Session
		}
		fmt.Printf("  %s  %-13s %s%s%s\n", when, e.Kind, e.Summary, where, sess)
	}
	fmt.Printf("\nRaw: %s\n", filepath.Join(vault, activity.Dir))
	return nil
}

func scopeLabel(q activity.Query) string {
	var parts []string
	if q.Project != "" {
		parts = append(parts, q.Project)
	}
	if q.Kind != "" {
		parts = append(parts, q.Kind)
	}
	if q.Tool != "" {
		parts = append(parts, q.Tool)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// emptyActivity distinguishes "nothing matched your filter" from "nothing is
// being recorded at all". They look the same and mean opposite things: one is a
// narrow question, the other is a feature that was never switched on.
func emptyActivity(vault string, q activity.Query) error {
	any, err := activity.Read(vault, activity.Query{Limit: 1})
	if err != nil {
		return err
	}
	if len(any) > 0 {
		fmt.Printf("nothing recorded%s.\n", scopeLabel(q))
		fmt.Println("  `brain activity --projects` lists what is being recorded.")
		return nil
	}
	fmt.Println("no activity recorded yet.")
	fmt.Println()
	fmt.Println("  Activity is written by the Claude Code hooks, so it starts filling in")
	fmt.Println("  the moment the plugin is installed and you begin a session:")
	fmt.Println()
	fmt.Println("      brain mcp install")
	fmt.Println()
	fmt.Println("  Unlike memories and checkpoints, nothing here depends on the model")
	fmt.Println("  choosing to record it — the host reports every prompt, tool call and")
	fmt.Println("  turn whether or not the agent would have thought to mention them.")
	return nil
}

func listActivityProjects(vault string) error {
	projects, err := activity.Projects(vault)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return emptyActivity(vault, activity.Query{})
	}
	fmt.Printf("%d %s with recorded activity\n\n", len(projects), plural(len(projects), "project"))
	for _, p := range projects {
		recent, err := activity.Read(vault, activity.Query{Project: p, Limit: 1})
		if err != nil || len(recent) == 0 {
			fmt.Printf("  %s\n", p)
			continue
		}
		n, _ := activity.Read(vault, activity.Query{Project: p})
		fmt.Printf("  %-24s %4d %-7s · last %s\n", p, len(n), plural(len(n), "event"),
			time.Unix(recent[0].TS, 0).Format("Jan 02 15:04"))
	}
	return nil
}

// recordActivity is the hook end. It reads a host's hook payload on stdin and
// appends one line.
//
// It never returns an error to the caller for anything but a genuinely broken
// invocation, and the hook script ignores even that. The rule is the same one
// the session hooks obey: recording a session must never be able to break the
// session. A lost line is a gap in the log; a non-zero exit here is a hook
// error in the user's face on every tool call.
func recordActivity(args []string) error {
	event := strings.TrimSpace(flagStr(args, "--event", ""))
	if event == "" {
		return fmt.Errorf("usage: brain activity record --event <HookEventName> < payload.json")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err != nil {
		return err
	}
	e, err := activity.FromHook(event, raw, strings.TrimSpace(flagStr(args, "--project", "")))
	if err != nil {
		return err
	}
	vault := vaultPath()
	if _, err := os.Stat(vault); err != nil {
		// No vault means Logos is not set up here. That is not an error worth
		// printing on every tool call of a session that never asked for it.
		return nil
	}
	return activity.Append(vault, e)
}

// plural saves the "1 projects" that makes a tool feel unfinished.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
