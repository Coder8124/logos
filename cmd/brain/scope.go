package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/brain/internal/rename"
	"github.com/Coder8124/brain/internal/scope"
	"github.com/Coder8124/brain/internal/session"
)

// runProjectName prints the project name for a directory, and nothing else.
//
// It exists so the shell hooks stop guessing. They used `basename "$PWD"`,
// which is the same rule internal/scope applies as a fallback but cannot see a
// .logos-project marker — so a repository that renamed itself was renamed for
// the MCP server and not for the hooks, and the two halves of continuity filed
// their work in two different places. One rule, one implementation, and the
// hooks read it from the binary rather than reimplementing it in bash.
//
// Prints nothing and exits 0 when the answer is global (no project), because a
// caller substituting this into a command line wants an empty string there, not
// the word "none" and not an error.
func runProjectName(args []string) error {
	dir := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			dir = a
			break
		}
	}
	if dir == "" {
		if d, err := os.Getwd(); err == nil {
			dir = d
		}
	}
	// BRAIN_PROJECT outranks the directory here exactly as it does in the MCP
	// server; a user who set it to make two folders share a project would
	// otherwise find the hooks ignoring it.
	name := strings.TrimSpace(os.Getenv("BRAIN_PROJECT"))
	if name == "" {
		name = scope.Name(dir)
	}
	if name != "" {
		fmt.Println(name)
	}
	return nil
}

// runProjectRename moves a project's whole history to a new name.
//
// The point of the command is that changing a name should be cheap. Before it
// existed the only safe move was to keep the old name forever — pinning it in
// .logos-project — because renaming the folder forked the history in a way
// nothing reported. That is a product telling the user their first guess at a
// name is permanent, which is not a thing a notebook does.
func runProjectRename(args []string) error {
	dryRun := hasFlag(args, "--dry-run")
	var names []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			names = append(names, a)
		}
	}
	if len(names) != 2 {
		return fmt.Errorf("usage: brain project rename <old> <new> [--dry-run]")
	}
	from, to := names[0], names[1]

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	// The sessions table is the one rewriteIndex touches that may not exist
	// yet, and it is cheaper to create it than to special-case its absence
	// halfway through a rename.
	if err := session.Init(ix.DB); err != nil {
		return err
	}

	res, err := rename.Run(ix.DB, ix.Vault, from, to, dryRun)
	if err != nil {
		return err
	}
	if res.Empty() {
		fmt.Printf("nothing filed under %q — check the name with `brain projects`\n", from)
		return nil
	}

	verb := "renamed"
	if dryRun {
		verb = "would rename"
	}
	fmt.Printf("%s %s → %s\n", verb, from, to)
	fmt.Printf("  %d checkpoint(s) in %s\n", res.Checkpoints, filepath.Join("sessions", from))
	fmt.Printf("  %d memory line(s), %d activity event(s), %d index row(s)\n", res.Memories, res.Events, res.Rows)
	if dryRun {
		fmt.Println("\nnothing was written. run again without --dry-run to apply.")
		return nil
	}
	// The marker is how the repository says which name is current, and a rename
	// that leaves it pointing at the old one undoes itself the next time a hook
	// runs. Say so rather than fixing a file outside the vault unasked.
	fmt.Printf("\nif this repo has a %s marker naming %q, update it — otherwise the next session files under the old name again.\n",
		scope.MarkerFile, from)
	fmt.Println("run `brain index` to refresh search over the moved notes.")
	return nil
}
