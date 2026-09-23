package main

import (
	"fmt"
	"strings"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/router"
	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// runTried asks whether an approach has already been ruled out — or, with
// --ruled-out, records that it now has been.
//
// The one command here that is not a lookup. Everything else answers a question
// the user thought to ask; this one is meant to be called *before* proposing
// something, by an agent that does not yet know there is anything to ask about.
//
// Recording lives here rather than in its own verb because the two halves are
// the same sentence at different moments: you ask `tried X` before you attempt
// X, and you say `tried X --ruled-out "why"` when X turns out not to work. The
// only other way in was `checkpoint --failed`, which meant a dead end found at
// 10am was invisible to every agent until the session ended — and invisible
// forever if the session ended without a checkpoint, which is how sessions
// usually end.
func runTried(args []string) error {
	proposed := strings.TrimSpace(strings.Join(firstNonFlags(args), " "))
	if proposed == "" {
		return fmt.Errorf("usage: logos tried <the approach you are about to propose> [--project X]\n       logos tried <the approach> --ruled-out <what happened> [--layer L] [--scope S] [--degree D] [--instead X]")
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if hasFlag(args, "--ruled-out") {
		return recordRuledOut(ix, args, proposed)
	}

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

	hits, semanticErr, err := deadend.CheckNoting(ix.Vault, ix.DB, embed, model, proposed, flagStr(args, "--project", ""), 6)
	if err != nil {
		return err
	}
	fmt.Print(deadend.Render(proposed, hits))
	fmt.Print(deadend.SemanticSkipped(semanticErr))
	if len(hits) > 0 {
		here := strings.TrimSpace(flagStr(args, "--project", ""))
		if here == "" {
			here = projectHere()
		}
		ledger(ix.Vault, usagepkg.Event{Kind: usagepkg.KindDeadEnd, Via: "cli:tried", Project: here, Rulings: len(hits)})
	}
	return nil
}

// recordRuledOut files the proposed approach as a dead end, now, without
// waiting for a checkpoint.
//
// It writes a working note in the typed record shape, which is the same shape
// `checkpoint --failed` entries parse into, so the ruling reads identically
// whichever way it arrived and `logos tried` finds it on the next call. A note
// rather than a new store because notes are already vault-backed and already
// folded into the next checkpoint — a dead end recorded here is promoted with
// the rest of the session's work rather than living somewhere only this command
// knows about.
func recordRuledOut(ix *index.Index, args []string, proposed string) error {
	why := strings.TrimSpace(flagStr(args, "--ruled-out", ""))
	if why == "" {
		return fmt.Errorf("--ruled-out needs what actually happened: logos tried %q --ruled-out \"the connection pool deadlocks above 40 workers\"", proposed)
	}
	project := strings.TrimSpace(flagStr(args, "--project", ""))
	if project == "" {
		project = projectHere()
	}
	if project == "" {
		return fmt.Errorf("no project here — name one with --project")
	}
	// The query half only ever reads, so this is the first command in its file
	// that needs the episodic tables to exist — on a vault whose owner has
	// never checkpointed, they do not.
	if err := session.Init(ix.DB); err != nil {
		return err
	}

	fields := []string{"route: " + proposed, "observation: " + why}
	// Each optional key is validated rather than passed through, because
	// ParseRecord drops a value it does not recognise: a typo'd layer would
	// record a ruling that silently lost the field the reader most needs, and
	// say nothing about it.
	for _, opt := range []struct{ flag, key, allowed string }{
		{"--layer", "layer", "implementation, design, environment, dependency, requirement"},
		{"--scope", "scope", "local, version-bound, general"},
		{"--degree", "degree", "contradicted, partial, inconclusive, unstable"},
		{"--action", "action", "retry, change-method, narrow-scope, abandon"},
	} {
		v := strings.TrimSpace(flagStr(args, opt.flag, ""))
		if v == "" {
			continue
		}
		if !strings.Contains(opt.allowed+",", v+",") {
			return fmt.Errorf("%s %q is not one of: %s", opt.flag, v, opt.allowed)
		}
		fields = append(fields, opt.key+": "+v)
	}
	if alt := strings.TrimSpace(flagStr(args, "--instead", "")); alt != "" {
		fields = append(fields, "alternative: "+alt)
	}

	if _, err := session.AddNote(ix.DB, project, agentName(), strings.Join(fields, " | ")); err != nil {
		return err
	}

	// Announced with the command that reads it back, because the whole value of
	// recording is that someone else's call finds it — and a ruling filed in
	// silence is indistinguishable from one that was not filed at all.
	fmt.Printf("ruled out on %s: %s\n", project, proposed)
	fmt.Printf("  because: %s\n", why)
	fmt.Printf("  the next `logos tried` or before_you_try on this approach will find it; it is folded into the next checkpoint.\n")
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
