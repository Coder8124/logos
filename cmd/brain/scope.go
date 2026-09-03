package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Coder8124/brain/internal/scope"
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
