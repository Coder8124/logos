package main

import (
	"fmt"
	"strings"
)

// The command bar is the "/" or ⌘K overlay: type a verb, get the same answer
// the CLI or the tab it mirrors would give. It carries zero business logic of
// its own — every entry's Run is a thin call into a method already bound on
// *App (Insights, Projects, Checkpoints, Memories, Status, GraphFind), the
// same ones the tabs already render from, so the bar can never show something
// the rest of the app would not.
//
// Deliberately left off the table: ContextPreview and Ask, which resolve a
// model through a.router(). Every command here must work identically on a
// machine with no model runtime configured, the same guarantee the rest of
// this package holds for Status (router.Load's error is swallowed there,
// never surfaced) — a command bar is the wrong place to introduce a first
// dependency on a runtime nothing else in this file requires.

// Command is one verb the bar can dispatch to.
type Command struct {
	Name  string `json:"name"`
	Usage string `json:"usage"`
	Run   func(a *App, arg string) (string, error)
}

// CommandResult is what the frontend renders after a dispatch. Verb is set
// whenever input resolved to a known command; Suggested carries the nearest
// known verb when it did not, so an unrecognized command is never silence —
// it names what the user probably meant instead of failing without a trace.
type CommandResult struct {
	Verb      string `json:"verb"`
	Output    string `json:"output"`
	Suggested string `json:"suggested"`
}

var commandTable = []Command{
	{
		Name:  "insights",
		Usage: "insights [project] — patterns already in the vault: a recurring blocker, a dormant memory",
		Run: func(a *App, arg string) (string, error) {
			view, err := a.Insights(arg)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "· %s\n", view.Degraded)
			fmt.Fprintf(&b, "· %d insight(s) found", len(view.Insights))
			if len(view.Drops) > 0 {
				fmt.Fprintf(&b, ", %d dropped for missing citations", len(view.Drops))
			}
			b.WriteString("\n")
			for _, in := range view.Insights {
				fmt.Fprintf(&b, "\n[%s] %s\n  source: %s\n", in.Kind, in.Text, strings.Join(in.Sources, ", "))
			}
			return b.String(), nil
		},
	},
	{
		Name:  "projects",
		Usage: "projects — every project with at least one checkpoint",
		Run: func(a *App, arg string) (string, error) {
			projects, err := a.Projects()
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "· %d project(s) found\n", len(projects))
			for _, p := range projects {
				fmt.Fprintf(&b, "  %s\n", p)
			}
			return b.String(), nil
		},
	},
	{
		Name:  "checkpoints",
		Usage: "checkpoints — every recorded checkpoint, newest first",
		Run: func(a *App, arg string) (string, error) {
			cps, err := a.Checkpoints()
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "· %d checkpoint(s) found\n", len(cps))
			for _, c := range cps {
				fmt.Fprintf(&b, "  %s (%s) — %s\n", c.Slug, c.Project, c.Next)
			}
			return b.String(), nil
		},
	},
	{
		Name:  "memories",
		Usage: "memories — what has been remembered so far",
		Run: func(a *App, arg string) (string, error) {
			mems, err := a.Memories()
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "· %d memor(y/ies) found\n", len(mems))
			for _, m := range mems {
				fmt.Fprintf(&b, "  #%d %s\n", m.ID, m.Text)
			}
			return b.String(), nil
		},
	},
	{
		Name:  "status",
		Usage: "status — vault counts and configured model runtime",
		Run: func(a *App, arg string) (string, error) {
			s, err := a.Status()
			if err != nil {
				return "", err
			}
			runtime := s.Runtime
			if runtime == "" {
				runtime = "none configured"
			}
			return fmt.Sprintf("· %d note(s), %d link(s), %d memor(y/ies) — runtime: %s",
				s.Notes, s.Edges, s.Memories, runtime), nil
		},
	},
	{
		Name:  "find",
		Usage: "find <query> — jump to the node the graph view would center on",
		Run: func(a *App, arg string) (string, error) {
			slug := a.GraphFind(arg)
			if slug == "" {
				return "· no matching node found", nil
			}
			return "· " + slug, nil
		},
	},
}

// Commands lists every known verb, for the frontend's "/" hint menu. It is a
// copy, not the live table, so a caller cannot reach in and replace a Run
// func from the frontend side.
func (a *App) Commands() []Command {
	out := make([]Command, len(commandTable))
	copy(out, commandTable)
	return out
}

// RunCommand splits input into a verb and the rest of the line, dispatches to
// the matching Command, and on no match names the nearest known verb rather
// than failing silently — an unrecognized command is exactly the moment a
// user most needs to be told what the tool actually understands.
func (a *App) RunCommand(input string) (CommandResult, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return CommandResult{}, nil
	}

	verb, arg, _ := strings.Cut(input, " ")
	arg = strings.TrimSpace(arg)

	for _, c := range commandTable {
		if c.Name == verb {
			out, err := c.Run(a, arg)
			if err != nil {
				return CommandResult{Verb: verb}, err
			}
			return CommandResult{Verb: verb, Output: out}, nil
		}
	}

	return CommandResult{Suggested: nearestCommand(verb)}, nil
}

// nearestCommand finds the known verb with the smallest edit distance to a
// typo, so "insigths" resolves to "insights" instead of dead-ending. Ties
// keep the first match in commandTable's declared order, which is stable
// across calls.
func nearestCommand(verb string) string {
	best := ""
	bestDist := -1
	for _, c := range commandTable {
		d := levenshtein(verb, c.Name)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = c.Name
		}
	}
	return best
}

// levenshtein is the classic single-row edit distance. Small alphabets and
// short strings only (command verbs), so the O(n*m) space is not worth
// trading away for readability here.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
