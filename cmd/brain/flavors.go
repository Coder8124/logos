package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pragun/brain/internal/bizagent"
	"github.com/pragun/brain/internal/business"
	"github.com/pragun/brain/internal/flavor"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/tutor"
)

// modeCmd switches or reports the active flavor.
func modeCmd(args []string) error {
	cfg, err := flavor.Load(vaultPath())
	if err != nil {
		return err
	}

	if len(args) == 0 {
		fmt.Printf("active: %s — %s\n\n", cfg.Active, cfg.Active.Describe())
		for _, f := range flavor.All() {
			mark := "  "
			if f == cfg.Active {
				mark = "* "
			}
			fmt.Printf("%s%-10s %s\n", mark, f, f.Describe())
		}
		return nil
	}

	f, err := flavor.Parse(args[0])
	if err != nil {
		return err
	}
	cfg.Active = f
	if err := cfg.Save(vaultPath()); err != nil {
		return err
	}
	fmt.Printf("switched to %s — %s\n", f, f.Describe())
	if f == flavor.Tutor && !cfg.ScreenNotes {
		fmt.Println("note: screen notes are off. `brain tutor screen on` to let it take notes off your screen.")
	}
	return nil
}

// tutorCmd drives study features: questions, summaries, screen toggle.
func tutorCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain tutor [study <topic> | quiz <topic> | screen on|off]")
	}

	switch args[0] {
	case "screen":
		return tutorScreenToggle(args[1:])
	case "study", "summary":
		return tutorStudy(strings.Join(args[1:], " "))
	case "quiz", "questions":
		return tutorQuiz(strings.Join(args[1:], " "))
	case "help":
		return tutorHelp()
	case "cards":
		return tutorCards(strings.Join(args[1:], " "))
	case "review":
		return tutorReview()
	case "diagnostic", "diagnose", "placement":
		return tutorDiagnostic(strings.Join(args[1:], " "))
	}
	return fmt.Errorf("usage: brain tutor [study <topic> | quiz <topic> | screen on|off]")
}

func tutorScreenToggle(args []string) error {
	cfg, err := flavor.Load(vaultPath())
	if err != nil {
		return err
	}
	if len(args) == 0 {
		state := "off"
		if cfg.ScreenNotes {
			state = "on"
		}
		fmt.Printf("screen notes: %s\n", state)
		return nil
	}
	cfg.ScreenNotes = args[0] == "on"
	if err := cfg.Save(vaultPath()); err != nil {
		return err
	}
	if cfg.ScreenNotes {
		fmt.Println("screen notes on. In tutor mode, `brain capture --daemon` will note studious screens.")
		fmt.Println("this needs Screen Recording permission and captures nothing outside tutor mode.")
	} else {
		fmt.Println("screen notes off.")
	}
	return nil
}

func tutorStudy(topic string) error {
	if topic == "" {
		return fmt.Errorf("usage: brain tutor study <topic>")
	}
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()
	rt, err := openRouter()
	if err != nil {
		return err
	}

	digest, sources, err := tutor.Summarize(ix, rt, topic)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s\n\n─── from ───\n", digest)
	for _, s := range sources {
		fmt.Printf("  %s\n", s)
	}
	return nil
}

func tutorQuiz(topic string) error {
	if topic == "" {
		return fmt.Errorf("usage: brain tutor quiz <topic>")
	}
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()
	rt, err := openRouter()
	if err != nil {
		return err
	}

	cards, err := tutor.Questions(ix, rt, topic, 5)
	if err != nil {
		return err
	}
	fmt.Printf("\n%d questions on %q — answers below each.\n\n", len(cards), topic)
	for i, c := range cards {
		fmt.Printf("%d. %s\n   → %s  [%s]\n\n", i+1, c.Q, c.A, c.Source)
	}
	return nil
}

// businessCmd drives MCP-backed data work.
func businessCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain business [tools | trends <question> | mcp add <name> <command...>]")
	}
	cfg, err := flavor.Load(vaultPath())
	if err != nil {
		return err
	}

	switch args[0] {
	case "mcp":
		return businessMCPAdd(cfg, args[1:])
	case "tools":
		return businessTools(cfg)
	case "trends":
		return businessTrends(cfg, strings.Join(args[1:], " "))
	case "read":
		return businessRead(args[1])
	case "analyze":
		return businessAnalyze(args[1], strings.Join(args[2:], " "))
	case "agent":
		return businessAgent(cfg, strings.Join(args[1:], " "))
	case "verify":
		return businessVerify(args[1])
	case "forecast":
		return businessForecast(args)
	}
	return fmt.Errorf("usage: brain business [tools | trends <q> | read <file> | analyze <file> [q] | agent <goal> | mcp add <name> <cmd...>]")
}

func businessMCPAdd(cfg *flavor.Config, args []string) error {
	if len(args) < 3 || args[0] != "add" {
		return fmt.Errorf("usage: brain business mcp add <name> <command> [args...]")
	}
	cfg.MCP = append(cfg.MCP, flavor.MCPServer{Name: args[1], Command: args[2], Args: args[3:]})
	if err := cfg.Save(vaultPath()); err != nil {
		return err
	}
	fmt.Printf("added MCP server %q\n", args[1])
	return nil
}

func businessTools(cfg *flavor.Config) error {
	if len(cfg.MCP) == 0 {
		return fmt.Errorf("no MCP servers — add one with `brain business mcp add <name> <command...>`")
	}
	tools, err := business.Discover(cfg.MCP)
	if err != nil {
		return err
	}
	for server, list := range tools {
		fmt.Printf("%s\n", server)
		for _, tl := range list {
			fmt.Printf("  %-24s %s\n", tl.Name, tl.Description)
		}
	}
	return nil
}

func businessTrends(cfg *flavor.Config, question string) error {
	if len(cfg.MCP) == 0 {
		return fmt.Errorf("no MCP servers — add one with `brain business mcp add <name> <command...>`")
	}

	// Call every tool on every configured server. For a focused query the user
	// can narrow the servers in config; this keeps the command itself simple.
	var calls []business.ToolCall
	discovered, err := business.Discover(cfg.MCP)
	if err != nil {
		return err
	}
	for server, list := range discovered {
		for _, tl := range list {
			calls = append(calls, business.ToolCall{Server: server, Tool: tl.Name})
		}
	}

	sources, err := business.Gather(cfg.MCP, calls)
	if err != nil {
		return err
	}
	rt, err := openRouter()
	if err != nil {
		return err
	}

	summary, err := business.TrendSummary(rt, question, sources)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s\n", strings.TrimSpace(summary))
	return nil
}

// thin aliases so main.go's tutorHelp does not import tutor directly.
func tutorLooksStudious(text string) bool { return tutor.LooksStudious(text) }

func tutorHelpText(rt *router.Router, text string) (string, error) { return tutor.Help(rt, text) }

// tutorCards generates questions on a topic and files them into the SRS deck.
func tutorCards(topic string) error {
	if topic == "" {
		return fmt.Errorf("usage: brain tutor cards <topic>")
	}
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := tutor.InitDeck(ix.DB); err != nil {
		return err
	}
	rt, err := openRouter()
	if err != nil {
		return err
	}

	cards, err := tutor.Questions(ix, rt, topic, 6)
	if err != nil {
		return err
	}
	added := 0
	for _, c := range cards {
		if ok, _ := tutor.AddCard(ix.DB, c); ok {
			added++
		}
	}
	fmt.Printf("added %d cards on %q to your deck — `brain tutor review` when they are due\n", added, topic)
	return nil
}

// tutorReview runs the spaced-repetition review of due cards.
func tutorReview() error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := tutor.InitDeck(ix.DB); err != nil {
		return err
	}

	now := time.Now()
	due, err := tutor.Due(ix.DB, now, 30)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		fmt.Println("no cards due — nothing to review.")
		return nil
	}

	fmt.Printf("%d cards due. For each: press enter to see the answer, then rate 1 (again) 2 (good) 3 (easy).\n\n", len(due))
	in := bufio.NewReader(os.Stdin)
	reviewed := 0

	for i, c := range due {
		fmt.Printf("[%d/%d] %s\n", i+1, len(due), c.Q)
		fmt.Print("      (enter to reveal) ")
		in.ReadString('\n')
		fmt.Printf("      → %s\n", c.A)

		fmt.Print("      rate 1/2/3 (q to stop): ")
		line, err := in.ReadString('\n')
		if err != nil {
			break
		}
		switch strings.TrimSpace(line) {
		case "1":
			tutor.Review(ix.DB, c.ID, tutor.Again, now)
		case "3":
			tutor.Review(ix.DB, c.ID, tutor.Easy, now)
		case "q":
			fmt.Printf("\nreviewed %d cards.\n", reviewed)
			return nil
		default:
			tutor.Review(ix.DB, c.ID, tutor.Good, now)
		}
		reviewed++
		fmt.Println()
	}
	fmt.Printf("reviewed %d cards.\n", reviewed)
	return nil
}

// jotCmd is braindump: capture a raw thought and let the system file it.
func tutorDiagnostic(subject string) error {
	if subject == "" {
		fmt.Println("pick a subject to place into:")
		for _, pr := range tutor.Presets {
			fmt.Printf("  brain tutor diagnostic %q\n", pr.Name)
		}
		fmt.Println("…or name any subject and it will build one.")
		return nil
	}
	// Use the canonical preset title in the output when the input matches one.
	if pr, ok := tutor.PresetFor(subject); ok {
		subject = pr.Name
	}
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := tutor.InitDeck(ix.DB); err != nil {
		return err
	}
	rt, err := openRouter()
	if err != nil {
		return err
	}

	fmt.Printf("· building a placement quiz for %q …\n", subject)
	subskills, err := tutor.Subskills(rt, subject)
	if err != nil {
		return err
	}
	fmt.Printf("· covering: %s\n· generating questions …\n\n", strings.Join(subskills, ", "))

	items, err := tutor.Diagnostic(rt, subject, subskills, 2)
	if err != nil {
		return err
	}

	in := bufio.NewReader(os.Stdin)
	answers := make([]int, len(items))
	for i, q := range items {
		fmt.Printf("[%d/%d] (%s) %s\n", i+1, len(items), q.Subskill, q.Q)
		for j, opt := range q.Options {
			fmt.Printf("    %d) %s\n", j+1, opt)
		}
		fmt.Print("    your answer (number, enter to skip): ")
		line, err := in.ReadString('\n')
		if err != nil {
			answers[i] = -1
			break
		}
		choice := -1
		if n := strings.TrimSpace(line); n != "" {
			fmt.Sscan(n, &choice)
			choice-- // 1-based to 0-based
		}
		answers[i] = choice
		fmt.Println()
	}

	m := tutor.Score(subject, items, answers)
	m.SortByLevel()

	fmt.Printf("\n─── where you stand in %s ───\n", subject)
	mark := map[tutor.Level]string{tutor.Gap: "○", tutor.Shaky: "◐", tutor.Solid: "●"}
	for _, s := range m.Scores {
		fmt.Printf("  %s %-28s %s  (%d/%d)\n", mark[s.Level], s.Subskill, s.Level, s.Correct, s.Total)
	}
	if m.StartHere != "" {
		fmt.Printf("\n→ start with: %s\n", m.StartHere)
	}

	// Close the loop: the gaps become spaced-repetition cards.
	weak := tutor.WeakCards(items, answers)
	added := 0
	for _, c := range weak {
		if ok, _ := tutor.AddCard(ix.DB, c); ok {
			added++
		}
	}
	if added > 0 {
		fmt.Printf("· added %d cards from what you missed — `brain tutor review` to practice them\n", added)
	}
	return nil
}

// businessRead prints the computed shape of a spreadsheet — no model, exact.
func businessRead(path string) error {
	if path == "" {
		return fmt.Errorf("usage: brain business read <file.xlsx|.csv>")
	}
	s, err := business.Summarize(path)
	if err != nil {
		return err
	}
	fmt.Print(s.String())
	return nil
}

// businessAnalyze narrates a spreadsheet's trends, grounded in computed figures.
func businessAnalyze(path, question string) error {
	if path == "" {
		return fmt.Errorf("usage: brain business analyze <file> [question]")
	}
	rt, err := openRouter()
	if err != nil {
		return err
	}
	out, err := business.AnalyzeFile(rt, path, question)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s\n", strings.TrimSpace(out))
	return nil
}

// businessAgent runs the tool-using harness toward a goal.
func businessAgent(cfg *flavor.Config, goal string) error {
	if goal == "" {
		return fmt.Errorf("usage: brain business agent <goal>")
	}
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()
	rt, err := openRouter()
	if err != nil {
		return err
	}

	reg := bizagent.NewRegistry()
	bizagent.RegisterBuiltins(reg)
	bizagent.RegisterTasks(reg)
	bizagent.RegisterGate(reg)                  // the agent can request outbound actions…
	bizagent.RegisterDefaultExecutors(ix.Vault) // …which only run once approved in `brain review`
	env := &bizagent.Env{Router: rt, Index: ix, DB: ix.DB, Vault: ix.Vault, MCP: cfg.MCP}
	runner := bizagent.NewRunner(env, reg)

	fmt.Printf("· working on: %s\n\n", goal)
	answer, err := runner.Run(goal, func(s bizagent.Step) {
		if s.Final != "" {
			return
		}
		fmt.Printf("  → %s(%s)\n", s.Tool, jsonArgsShort(s.Args))
	})
	if err != nil {
		return err
	}
	fmt.Printf("\n%s\n", strings.TrimSpace(answer))
	return nil
}

func jsonArgsShort(args map[string]any) string {
	var parts []string
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ", ")
}

// businessVerify runs the deterministic finance checks.
func businessVerify(path string) error {
	if path == "" {
		return fmt.Errorf("usage: brain business verify <file>")
	}
	rep, err := business.Verify(path)
	if err != nil {
		return err
	}
	fmt.Print(rep.String())
	if rep.Failed > 0 {
		fmt.Printf("\n%d check(s) need attention.\n", rep.Failed)
	}
	return nil
}

// businessForecast projects a column forward, computed exactly.
func businessForecast(args []string) error {
	// args: forecast <file> <column> [--periods N] [--method cagr|linear]
	if len(args) < 3 {
		return fmt.Errorf("usage: brain business forecast <file> <column> [--periods N] [--method cagr|linear]")
	}
	path, column := args[1], args[2]
	periods := flagInt(args, "--periods", 4)
	method := flagStr(args, "--method", "cagr")
	p, err := business.ForecastFile(path, column, periods, method)
	if err != nil {
		return err
	}
	fmt.Print(p.String())
	return nil
}
