package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/contextpack"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/provider"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/session"
)

// `brain demo` — the ninety seconds that show what this is for.
//
// The demo it replaces was "start a session, quit halfway, resume somewhere
// else", and that demo does not work. Not because it fails, but because the
// audience cannot tell whether it succeeded: they never saw what the first
// session knew, so a confident second session is indistinguishable from a
// fluent one. Coding agents sound competent when they are wrong — that is the
// thing being sold against, and it sabotages the pitch too.
//
// So this runs the failure first. The room watches an agent give the stale
// answer, then the same question asked through Logos, then the receipt showing
// exactly when the value moved and what replaced it. The audience knows the
// right answer before the answer arrives, which is the only way a memory demo
// is ever checkable.
//
// It uses a scratch vault and touches nothing real, it makes no model calls,
// and it takes the same path every time. A demo that needs the network is a
// demo that fails in the one room that mattered.
func runDemo(args []string) error {
	slow := !hasFlag(args, "--fast")
	vault := strings.TrimSpace(flagStr(args, "--vault", ""))
	if vault == "" {
		dir, err := os.MkdirTemp("", "logos-demo-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		vault = dir
	} else if err := os.MkdirAll(vault, 0o755); err != nil {
		return err
	}

	// A scratch vault, stated out loud. Somebody is about to run this on their
	// own laptop during a call and needs to know their real vault is not being
	// written to.
	say(slow, "\n  Logos demo · scratch vault at %s\n  Nothing here touches your own vault.\n", vault)

	ix, err := openEventsAt(vault)
	if err != nil {
		return err
	}
	defer ix.Close()
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			return err
		}
	}

	// The router is resolved before anything is stored, because the memories
	// have to be embedded on the way in — consolidation compares vectors, and a
	// memory stored without one is invisible to it. Discovering that at step 4,
	// after the audience has watched two facts go in, is the worst possible
	// moment to find out the demo cannot finish.
	rt, rtErr := openRouterOptional()
	if rtErr != nil || rt == nil {
		fmt.Println("  This demo needs the local embedding model: supersession compares")
		fmt.Println("  meaning, not text, and there is nothing to compare without it.")
		fmt.Println()
		fmt.Println("      brain setup")
		fmt.Println()
		fmt.Println("  It runs entirely on your machine — no network, no API key.")
		return nil
	}
	embed, embedModel := rt.Local(), ""
	if m, err := rt.Model(router.T0); err == nil {
		embedModel = m
	}

	step(slow, 1, "Monday. An agent learns how staging is configured.")
	say(slow, "      > \"staging runs on port 8080\"\n")
	first, err := demoRemember(ix.DB, embed, embedModel, "Staging runs on port 8080.")
	if err != nil {
		return err
	}
	say(slow, "      %s\n", first)

	step(slow, 2, "Thursday. It changes. A different session, a different agent.")
	say(slow, "      > \"we moved staging to port 9090\"\n")
	second, err := demoRemember(ix.DB, embed, embedModel, "Staging runs on port 9090.")
	if err != nil {
		return err
	}
	say(slow, "      %s\n", second)

	step(slow, 3, "Both facts are now in the vault, written weeks apart.")
	say(slow, "      This is where the competition stops: two values for one thing,\n"+
		"      both retrievable, nothing saying which is true. Whichever embeds\n"+
		"      closer to the question wins, and that is not the same as correct.\n")

	// The real consolidation pass, not a staged one. It needs the local
	// embedding model — no network, but a model — and when there is none the
	// demo says so rather than pretending. A rigged demo of the one claim this
	// product is built on would be a strange thing to rig.
	step(slow, 4, "Logos runs consolidation. This is the part nobody else does.")
	_, superseded, err := memory.Consolidate(ix.DB, rt)
	if err != nil {
		return err
	}
	if superseded == 0 {
		fmt.Println()
		fmt.Println("      The model did not judge these the same fact, so nothing was")
		fmt.Println("      superseded. That is a real outcome, not a demo failure — and it")
		fmt.Println("      is why the receipt below is worth more than a claim.")
	} else {
		say(slow, "      ✓ Logos · %d memory superseded — the old value is now history\n", superseded)
	}

	step(slow, 5, "Friday. A third agent asks what it needs to deploy.")
	say(slow, "      > context(\"deploy to staging\")\n\n")
	pack, err := contextpack.Build(ix, embed, embedModel, contextpack.Request{
		Task: "deploy to staging", Budget: 2000,
	})
	if err != nil {
		return err
	}
	shown := false
	for _, line := range strings.Split(strings.TrimSpace(pack.Render()), "\n") {
		if strings.Contains(line, "8080") || strings.Contains(line, "9090") {
			say(slow, "      %s\n", line)
			shown = true
		}
	}
	if !shown {
		say(slow, "      (no port line surfaced — say so out loud rather than skipping past it)\n")
	}
	// Naming the withheld value is the whole demo. A pack that silently drops
	// the stale answer looks identical to a pack that never had it.
	if n := len(pack.Superseded); n > 0 {
		say(slow, "\n      Held back as superseded: %d\n", n)
		for _, m := range pack.Superseded {
			say(slow, "        × %s\n", m.Text)
		}
	}

	step(slow, 6, "The receipt. Not a claim — the history of the fact itself.")
	if err := demoHistory(ix.DB, slow); err != nil {
		return err
	}

	fmt.Println()
	if superseded > 0 {
		fmt.Println("  What just happened, in one sentence:")
		fmt.Println("    the old value was not ranked lower. It was withheld, and the")
		fmt.Println("    reason it was withheld is on the record.")
	} else {
		fmt.Println("  The model did not judge those the same fact this time, so nothing")
		fmt.Println("  was superseded. Said plainly rather than glossed over: a demo that")
		fmt.Println("  narrates an outcome it did not produce is worth nothing.")
	}
	fmt.Println()
	fmt.Println("  Try it against your own repository, no setup, nothing written:")
	fmt.Println("    brain bootstrap --dry-run")
	fmt.Println()
	return nil
}

func demoRemember(db *sql.DB, embed *provider.Provider, model, text string) (string, error) {
	r, err := memory.Store(db, embed, model, &memory.Memory{
		Text: text, Kind: memory.Fact, Salience: 0.8, Confidence: 0.9,
		Source: "demo", Created: time.Now().Unix(),
	})
	if err != nil {
		return "", err
	}
	if r.Created() {
		return fmt.Sprintf("✓ Logos · stored in brain — memory #%d", r.ID), nil
	}
	return fmt.Sprintf("✓ Logos · already knew that — reinforced memory #%d", r.Ref), nil
}

func demoHistory(db *sql.DB, slow bool) error {
	entries, err := memory.Timeline(db, 0)
	if err != nil {
		return err
	}
	// Oldest first here, unlike everywhere else: this is a story about a value
	// moving, and a story told backwards does not land.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		ref := ""
		if e.RefID != 0 {
			ref = fmt.Sprintf(" → #%d", e.RefID)
		}
		say(slow, "      %-11s #%d  %s%s\n", e.Event, e.MemID, truncateLine(e.Detail, 44), ref)
	}
	return nil
}

func step(slow bool, n int, title string) {
	fmt.Printf("\n  %d. %s\n", n, title)
	pause(slow, 900*time.Millisecond)
}

// say prints and then waits, so a room can read a line before the next one
// lands. --fast removes every pause for anyone running this in CI or for the
// tenth time today.
func say(slow bool, format string, a ...any) {
	fmt.Printf(format, a...)
	pause(slow, 450*time.Millisecond)
}

func pause(slow bool, d time.Duration) {
	if slow {
		time.Sleep(d)
	}
}

func openEventsAt(vault string) (*index.Index, error) {
	if err := os.MkdirAll(filepath.Join(vault, ".brain"), 0o755); err != nil {
		return nil, err
	}
	old := os.Getenv("BRAIN_VAULT")
	os.Setenv("BRAIN_VAULT", vault)
	defer os.Setenv("BRAIN_VAULT", old)
	return openEvents()
}
