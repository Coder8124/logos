package main

import (
	"fmt"
	"strings"

	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/project"
	"github.com/pragun/brain/internal/router"
)

// runContext assembles a context pack for a file, project, or topic and prints it
// as markdown — the same bundle the MCP `context_pack` tool serves to an external
// AI. "Here is a file" becomes "here is everything the assistant knows that bears
// on it," in one shot.
func runContext(hint string) error {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return fmt.Errorf("usage: brain context <file | project | topic>")
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}

	rt, err := openRouter()
	if err != nil {
		return err
	}
	embedModel, _ := rt.Model(router.T0)

	pack, err := project.BuildContext(ix.DB, rt.Local(), embedModel, hint)
	if err != nil {
		return err
	}
	fmt.Print(pack.Render())
	return nil
}
