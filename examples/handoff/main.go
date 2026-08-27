// Command handoff is the smallest useful embedding of brain: two agents, one
// vault, no shared process. The first works and stops; the second arrives cold
// and continues.
//
// Run it against a scratch vault — it writes checkpoints:
//
//	mkdir -p /tmp/demo-vault
//	go run ./examples/handoff /tmp/demo-vault
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/pragun/brain"
)

const project = "kestrel-one"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: handoff <vault-path>")
	}
	vault := os.Args[1]

	first(vault)
	fmt.Println("\n--- the first agent is gone ---")
	second(vault)
}

// first is an agent doing work. It records progress as it goes and commits
// where it stopped — including, crucially, what did not work.
func first(vault string) {
	b, err := brain.Open(vault, brain.WithAgent("claude"))
	if err != nil {
		log.Fatal(err)
	}
	defer b.Close()

	b.Note(project, "re-quoted the waveguide; no movement under 10k units")

	slug, err := b.Checkpoint(brain.Checkpoint{
		Project:   project,
		Task:      "cut the BOM to the $38 target",
		State:     "BOM lands at $42.20 — $4.20 over",
		Decisions: []string{"hold the aluminium frame; the drop test depends on it"},
		Failed:    []string{"re-quoting the waveguide — no movement under 10k units"},
		Next:      "quote the display driver alternatives",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("checkpointed to", slug)
}

// second is a different agent — a different product, on a different day — that
// has never seen this project.
func second(vault string) {
	b, err := brain.Open(vault, brain.WithAgent("cursor"))
	if err != nil {
		log.Fatal(err)
	}
	defer b.Close()

	c, err := b.Resume(project)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(c.Render()) // drop this straight into a system prompt

	// Before proposing anything, check nobody has already ruled it out. This
	// searches every dead end in the vault, not just this project's.
	approach := "re-quoting the waveguide to bring the BOM down"
	ruled, err := b.Tried(approach, project)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(brain.Explain(approach, ruled))
}
