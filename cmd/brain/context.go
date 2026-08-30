package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pragun/brain/internal/contextpack"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
)

// runContext assembles everything bearing on a task and prints it as markdown —
// the same bundle the MCP `context` tool serves to an external AI. Having it on
// the command line is not a convenience: it is the only way to see what an agent
// will actually receive, and therefore the only way to tell whether the
// retrieval is any good.
//
//	brain context "cut the BOM to target" --project kestrel-one --budget 4000
func runContext(args []string) error {
	var task, hint string
	budget := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				hint = args[i]
			}
		case "--budget", "-b":
			if i+1 < len(args) {
				i++
				budget, _ = strconv.Atoi(args[i])
			}
		default:
			task = strings.TrimSpace(task + " " + args[i])
		}
	}
	if task == "" && hint == "" {
		return fmt.Errorf("usage: brain context <task> [--project <name>] [--budget <tokens>]")
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}
	if err := session.Init(ix.DB); err != nil {
		return err
	}
	if err := secretary.Init(ix.DB); err != nil {
		return err
	}

	// contextpack.Build treats a nil provider as "skip the semantic arm", so a
	// machine with no runtime still gets the checkpoint, the dead ends, the open
	// loops and the graph-reached notes — which is most of what context is for.
	rt, err := openRouterOptional()
	if err != nil {
		return err
	}
	var embed *provider.Provider
	var embedModel string
	if rt != nil {
		embedModel, _ = rt.Model(router.T0)
		embed = rt.Local()
	} else {
		fmt.Fprintln(os.Stderr, "· no model runtime — assembling without semantic search")
	}

	pack, err := contextpack.Build(ix, embed, embedModel,
		contextpack.Request{Task: task, Hint: hint, Budget: budget})
	if err != nil {
		return err
	}
	fmt.Print(pack.Render())
	return nil
}
