package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/bootstrap"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
)

// `logos bootstrap` seeds a cold vault from the repository's own git history.
//
// It exists for the first session on an existing project, where memory is empty
// not because nothing is known but because nothing has been written down yet.
// See internal/bootstrap for what it will and will not claim.
//
// Nothing is written without a yes. That is the same rule `logos setup` follows
// for wiring hosts, and it matters more here: these memories are about to be
// recalled into an agent's context as if a human had asserted them, so the human
// gets to read them first.
func runBootstrap(args []string) error {
	dir := flagStr(args, "--dir", "")
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}
	months := flagInt(args, "--months", 12)
	dryRun := hasFlag(args, "--dry-run")
	assumeYes := hasFlag(args, "--yes")

	project := strings.TrimSpace(flagStr(args, "--project", ""))
	if project == "" {
		project = firstNonFlag(args)
	}
	if project == "" {
		// The same rule the MCP server uses: a project is the folder's name, so
		// the CLI and an agent working in that folder land in one scope rather
		// than two. See internal/mcpserver/scope.go.
		project = filepath.Base(filepath.Clean(dir))
	}

	found, unread := bootstrap.FromGitHistory(dir, months)
	// Before either verdict: "nothing to seed" about a repository git refused
	// to read is a claim about its history that nobody checked (#204).
	for _, what := range unread {
		fmt.Printf("git refused to read %s here, so nothing below is drawn from it.\n", what)
	}
	if len(unread) > 0 {
		fmt.Println()
	}
	if len(found) == 0 {
		// Not an error. A shallow clone, a young repository, or a directory that
		// is not a repository at all are all legitimate — and seeding something
		// anyway is the failure mode this command is written to avoid.
		fmt.Printf("Nothing to seed from %s.\n", dir)
		fmt.Println("  A repository needs a real history before it can say anything general;")
		fmt.Println("  below that, anything derived would be one afternoon mistaken for a pattern.")
		return nil
	}

	fmt.Printf("From the git history of %s, scoped to project %q:\n\n", dir, project)
	printSeeds(found)

	if dryRun {
		fmt.Printf("%d memories would be written. Nothing was.\n", len(found))
		return nil
	}
	if !assumeYes && !confirmBootstrap(len(found)) {
		fmt.Println("Nothing written.")
		return nil
	}
	return storeSeeds(found, project)
}

func printSeeds(found []bootstrap.Candidate) {
	for i, c := range found {
		fmt.Printf("  %d. %s\n", i+1, c.Text)
		fmt.Printf("     %s · confidence %.2f\n\n", c.Evidence, c.Confidence)
	}
}

// storeSeeds writes what the user said yes to, and says how to take it back.
func storeSeeds(found []bootstrap.Candidate, project string) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := memory.Init(ix.DB); err != nil {
		return err
	}

	// Embedding is what lets these dedup against each other on a second run and
	// against anything the user later states by hand. Without a runtime they are
	// still written — continuity does not require a model here any more than it
	// does anywhere else — they just dedup by exact text instead.
	var embed *provider.Provider
	var model string
	if rt, err := openRouter(); err == nil {
		if m, err := rt.Model(router.T0); err == nil {
			embed, model = rt.Local(), m
		}
	}

	var created, reinforced int
	for _, c := range found {
		m := &memory.Memory{
			Text:       c.Text,
			Kind:       c.Kind,
			Salience:   c.Salience,
			Confidence: c.Confidence,
			Project:    project,
			Source:     bootstrap.Source,
		}
		r, err := memory.Store(ix.DB, embed, model, m)
		if err != nil {
			return fmt.Errorf("storing %q: %w", c.Text, err)
		}
		if r.Created() {
			created++
		} else {
			reinforced++
		}
	}

	fmt.Printf("\n%d written, %d already known.\n", created, reinforced)
	if embed == nil {
		fmt.Println("No embedding model, so these were matched by exact text; a later run with one will merge near-duplicates.")
	}
	fmt.Printf("To undo all of it: logos memory forget --source %s\n", bootstrap.Source)
	return nil
}

// confirmBootstrap reads through setup's one shared scanner: setup offers
// bootstrap between its own prompts, and a second reader on stdin would read
// ahead and swallow the answers meant for them.
func confirmBootstrap(n int) bool {
	return confirmNo(fmt.Sprintf("Write these %d memories?", n))
}
