package main

import (
	"fmt"

	"github.com/Coder8124/brain/internal/session"
)

// `brain plans <project>` — where an approved plan-mode plan ends up, since
// the ExitPlanMode hook that saves it (cmd/brain/activity.go's recordActivity)
// runs silently by design, the same as the rest of activity capture. Every
// silent feature in this codebase needs a place a person can go look; this is
// that place for 0.8.
func runPlans(args []string) error {
	project, _ := projectArg(args)
	if project == "" {
		return fmt.Errorf("usage: brain plans <project>")
	}

	vault, err := requireVault()
	if err != nil {
		return err
	}
	plans, err := session.ListPlans(vault, project)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Printf("no plans captured for %s yet.\n", project)
		fmt.Println()
		fmt.Println("  A plan is saved automatically the moment Claude Code's plan mode is")
		fmt.Println("  approved — nothing to run by hand, but it needs the hooks installed:")
		fmt.Println()
		fmt.Println("      brain mcp install")
		return nil
	}

	fmt.Printf("%d %s for %s\n\n", len(plans), plural(len(plans), "plan"), project)
	for _, p := range plans {
		who := p.Agent
		if who == "" {
			who = "agent"
		}
		fmt.Printf("%s  %s\n", p.Slug, who)
		fmt.Printf("    %s\n", oneLineOf(p.Text))
	}
	return nil
}
