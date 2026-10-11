package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/scope"
	"github.com/Coder8124/logos/internal/untrusted"
)

// recallCmd is the MCP recall tool from a shell. `logos search` reaches
// memories too, but ranked among notes and without the review queue's report,
// so a person checking what an agent will be told had no way to see it.
func recallCmd(args []string) error {
	if err := checkCommandFlags("recall", args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(dropFlag(dropFlag(positionals(args, "--all-projects"), "--project"), "--limit"), " "))
	if query == "" {
		return fmt.Errorf("usage: logos recall <query> [--project <name>] [--all-projects] [--limit N]")
	}
	project := ""
	if !hasFlag(args, "--all-projects") {
		project = scope.NormalizeArg(flagStr(args, "--project", ""))
		if project == "" {
			project = projectHere()
		}
	}
	ix, err := openIndex()
	if err != nil {
		return err
	}
	defer ix.Close()
	var embed *provider.Provider
	model, embedOn := embedModel()
	if embedOn {
		if p, perr := findProvider(); perr == nil {
			embed = p
		} else {
			fmt.Fprintln(os.Stderr, "· no model runtime — recalling by keyword")
		}
	}
	r, err := ops.Recall(ix.DB, ops.RecallQuery{
		Query: query, Limit: flagInt(args, "--limit", 5), Project: project,
		Embed: embed, Model: model, Shell: "logos",
	})
	if err != nil {
		return err
	}
	ifEmpty := "No relevant memories."
	if project != "" {
		ifEmpty = fmt.Sprintf("No relevant memories in %s. Pass --all-projects to search every project.", project)
		if len(r.Memories) == 0 && !ops.ProjectExists(ix.DB, vaultPath(), project) {
			ifEmpty = fmt.Sprintf("No project named %s in this vault.", untrusted.Inline(project))
		}
	}
	fmt.Println(r.Text(ifEmpty))
	return nil
}
