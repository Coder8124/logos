package main

import (
	"fmt"
	"strings"

	"github.com/Coder8124/brain/internal/insight"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/session"
)

// runInsights prints what internal/insight can find by pattern-matching the
// vault: a blocker that has outlived several checkpoints, a memory nobody has
// drawn on in months. It mirrors runContext's open/init sequence rather than
// inventing a second one, and — like runContext degrading without a model
// runtime — it prints insight.Degraded up front, because the package has only
// ever shipped the mechanical tier and a caller has no other way to know that.
//
//	brain insights [project]
func runInsights(args []string) error {
	var project string
	if len(args) > 0 {
		project = args[0]
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

	insights, drops, err := insight.Generate(ix.DB, ix.Vault, project)
	if err != nil {
		return err
	}

	fmt.Println("·", insight.Degraded)
	// Invariant 3: the count is the headline, not a fact buried in a list —
	// "0 insights" on a quiet vault must read as "looked, found nothing", not
	// as a command that silently declined to run.
	fmt.Printf("· %d insight(s) found", len(insights))
	if len(drops) > 0 {
		fmt.Printf(", %d dropped for missing citations", len(drops))
	}
	fmt.Println()

	for _, in := range insights {
		fmt.Printf("\n[%s] %s\n", in.Kind, in.Text)
		fmt.Printf("  source: %s\n", strings.Join(in.Sources, ", "))
	}
	return nil
}
