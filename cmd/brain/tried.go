package main

import (
	"fmt"
	"strings"

	"github.com/pragun/brain/internal/deadend"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/router"
)

// runTried asks whether an approach has already been ruled out.
//
// The one command here that is not a lookup. Everything else answers a question
// the user thought to ask; this one is meant to be called *before* proposing
// something, by an agent that does not yet know there is anything to ask about.
func runTried(args []string) error {
	proposed := strings.TrimSpace(strings.Join(firstNonFlags(args), " "))
	if proposed == "" {
		return fmt.Errorf("usage: brain tried <the approach you are about to propose> [--project X]")
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	// Degrade rather than refuse. Without an embedder the lexical arm still
	// catches a proposal that restates the original, which is the common case
	// when an agent is working from the same notes the dead end was written in.
	var embed *provider.Provider
	var model string
	if rt, err := openRouter(); err == nil {
		if m, err := rt.Model(router.T0); err == nil {
			embed, model = rt.Local(), m
		}
	}

	hits, err := deadend.Check(ix.Vault, ix.DB, embed, model, proposed, flagStr(args, "--project", ""), 6)
	if err != nil {
		return err
	}
	fmt.Print(deadend.Render(proposed, hits))
	return nil
}

// firstNonFlags returns the leading arguments before any flag, so the approach
// can be typed without quoting every word.
func firstNonFlags(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			break
		}
		out = append(out, a)
	}
	return out
}
