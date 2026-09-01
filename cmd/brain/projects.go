package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/project"
)

// projectsCmd surfaces the work the system has detected on its own — each with
// the notes, people, files, goals, and recent progress it has gathered, none of
// which the user had to file.
func projectsCmd(args []string) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if len(args) >= 1 && args[0] == "sync" {
		n, err := project.AutoScope(ix.DB)
		if err != nil {
			return err
		}
		fmt.Printf("scoped %d memories to their project.\n", n)
		return nil
	}

	ps, err := project.Detect(ix.DB)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		b, _ := json.MarshalIndent(ps, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(ps) == 0 {
		fmt.Println("no projects detected yet — they emerge as the rollup files your activity into project notes.")
		return nil
	}
	fmt.Printf("%d projects detected\n\n", len(ps))
	for _, p := range ps {
		fmt.Printf("  %-22s %s\n", p.Name, project.Age(p.LastActive))
		fmt.Printf("    %d notes · %d people · %d files · %d goals · %d memories · %d conversations\n",
			len(p.Notes), len(p.People), len(p.Files), len(p.Goals), len(p.Memories), p.Convos)
	}
	fmt.Println("\ntip: `brain project <name>` for the full dossier · `brain projects sync` to scope memory")
	return nil
}

// projectCmd prints one project's full dossier.
func projectCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: brain project <name>")
	}
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	name := strings.Join(args, " ")
	p, ok, err := project.Get(ix.DB, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no project matching %q — try `brain projects`", name)
	}

	fmt.Printf("# %s", p.Name)
	if len(p.Aliases) > 0 {
		fmt.Printf("  (also: %s)", strings.Join(p.Aliases, ", "))
	}
	fmt.Printf("\n  last active %s\n", project.Age(p.LastActive))

	section("Goals", len(p.Goals) == 0)
	for _, g := range p.Goals {
		fmt.Printf("  ◦ %s\n", g)
	}

	section("People", len(p.People) == 0)
	for _, r := range p.People {
		fmt.Printf("  • %s\n", r.Title)
	}

	section("Recent progress", len(p.Progress) == 0)
	for _, pr := range p.Progress {
		fmt.Printf("  %-8s %s  %s\n", project.Age(pr.TS), "["+pr.Kind+"]", truncateLine(pr.Text, 60))
	}

	section("Files", len(p.Files) == 0)
	for _, f := range p.Files {
		fmt.Printf("  %s  (%d× · %s)\n", truncateLine(f.Path, 52), f.Count, project.Age(f.LastTS))
	}

	section("Notes", len(p.Notes) == 0)
	for _, r := range p.Notes {
		fmt.Printf("  %-12s %s\n", "["+r.Kind+"]", r.Title)
	}

	section(fmt.Sprintf("Project memory (%d)", len(p.Memories)), len(p.Memories) == 0)
	for _, m := range p.Memories {
		fmt.Printf("  (%-10s conf %.2f) %s\n", m.Kind, m.Confidence, truncateLine(m.Text, 60))
	}
	return nil
}

func section(title string, empty bool) {
	fmt.Printf("\n%s\n", title)
	if empty {
		fmt.Println("  —")
	}
}
