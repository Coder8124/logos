package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/secretary"
	"github.com/Coder8124/brain/internal/session"
)

// runContext assembles everything bearing on a task and prints it as markdown —
// the same bundle the MCP `context` tool serves to an external AI. Having it on
// the command line is not a convenience: it is the only way to see what an agent
// will actually receive, and therefore the only way to tell whether the
// retrieval is any good.
//
//	brain context "cut the BOM to target" --project kestrel-one --budget 4000 --since week
//
// --pin/--exclude/--unpin and --rules manage the tree-view's durable state
// from the command line, the same file the desktop app's folder tree reads
// and writes — one vocabulary, not a CLI copy of an app-only feature.
//
//	brain context --pin sessions/old-project
//	brain context --exclude memories/context.md
//	brain context --rules
func runContext(args []string) error {
	var task, hint, since string
	var pin, exclude, unpin string
	var listRules bool
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
		case "--since":
			if i+1 < len(args) {
				i++
				since = args[i]
			}
		case "--pin":
			if i+1 < len(args) {
				i++
				pin = args[i]
			}
		case "--exclude":
			if i+1 < len(args) {
				i++
				exclude = args[i]
			}
		case "--unpin":
			if i+1 < len(args) {
				i++
				unpin = args[i]
			}
		case "--rules":
			listRules = true
		default:
			task = strings.TrimSpace(task + " " + args[i])
		}
	}

	if pin != "" || exclude != "" || unpin != "" || listRules {
		return runContextRules(pin, exclude, unpin, listRules)
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
		contextpack.Request{Task: task, Hint: hint, Budget: budget, Since: contextpack.Since(since)})
	if err != nil {
		return err
	}
	fmt.Print(pack.Render())
	return nil
}

// runContextRules manages the tree view's durable pin/exclude state. It writes
// the vault, not the index — invariant 1 — so this must work even against a
// vault whose .brain/index.db was just deleted; nothing here reads it.
func runContextRules(pin, exclude, unpin string, list bool) error {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}

	switch {
	case pin != "":
		if err := contextpack.SetPathRule(v, pin, contextpack.PathPinAlways); err != nil {
			return err
		}
		fmt.Printf("· pinned %s — always included in a context pack\n", pin)
	case exclude != "":
		if err := contextpack.SetPathRule(v, exclude, contextpack.PathPinNever); err != nil {
			return err
		}
		fmt.Printf("· excluded %s — never included in a context pack\n", exclude)
	case unpin != "":
		if err := contextpack.SetPathRule(v, unpin, contextpack.PathPinNone); err != nil {
			return err
		}
		fmt.Printf("· cleared the rule on %s\n", unpin)
	}

	if !list {
		return nil
	}
	rules, err := contextpack.LoadPathRules(v)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		fmt.Println("· no path rules set — every directory is ranked normally")
		return nil
	}
	for _, r := range rules {
		verb := "pin"
		if r.Pin == contextpack.PathPinNever {
			verb = "exclude"
		}
		fmt.Printf("%s: %s\n", verb, r.Prefix)
	}
	return nil
}
