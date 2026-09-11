package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
)

// The trust loop. Everything a model infers about you passes through here
// before it touches the vault.
//
// This file used to also run `brain rollup` and review its proposed vault
// notes, mined from ambient capture. That half was cut in 0.3.0 along with the
// rest of the ambient-capture tier. What remains —
// Stage 4's quarantined-memory review — is unrelated to it: it is the queue
// the `remember` MCP tool fills so no agent writes straight into the vault
// (internal/mcpserver's quarantine handling, internal/memory/quarantine.go),
// and this is the only place in the codebase that ever resolves it.

// runReview accepts or rejects memories waiting in quarantine.
func runReview(all bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := memory.Init(ix.DB); err != nil {
		return err
	}

	memories, err := memory.Pending(ix.DB)
	if err != nil {
		return err
	}
	if len(memories) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	accepted, rejected, skipped, _ := reviewMemories(ix, in, memories, all)

	fmt.Printf("%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
	if accepted > 0 {
		fmt.Println("run `brain index` to pick up the new notes")
	}
	return nil
}

// reviewMemories runs the accept/reject/skip loop over Stage 4's
// quarantined memories. Accept releases the memory into active memory and
// flushes it to the vault; reject discards it for good. There is no
// "evidence" view here the way rollup's now-deleted proposal review had one —
// a memory is already the atomic fact, not a summary of events behind it.
func reviewMemories(ix *index.Index, in *bufio.Reader, pending []memory.Memory, all bool) (accepted, rejected, skipped int, quit bool) {
	fmt.Printf("%d pending memor%s · [a]ccept  [r]eject  [s]kip  [q]uit\n\n", len(pending), pluralY(len(pending)))

	for i, m := range pending {
		if !all && i >= 20 {
			fmt.Printf("\n… %d more, run again to continue\n", len(pending)-i)
			break
		}

		fmt.Printf("[%d/%d] (%s) %s\n", i+1, len(pending), m.Kind, m.Text)
		fmt.Printf("        conf %.2f · %s\n", m.Confidence, m.Source)

		for {
			fmt.Print("      > ")
			line, err := in.ReadString('\n')
			if err != nil {
				fmt.Println()
				return accepted, rejected, skipped, true
			}

			switch strings.TrimSpace(strings.ToLower(line)) {
			case "a", "y":
				if err := memory.Accept(ix.DB, m.ID); err != nil {
					fmt.Printf("      ! %v\n", err)
					break
				}
				accepted++
				fmt.Println("      ✓ accepted")
			case "r", "n":
				if err := memory.Reject(ix.DB, m.ID); err != nil {
					fmt.Printf("      ! %v\n", err)
					break
				}
				rejected++
				fmt.Println("      ✗ rejected")
			case "s", "":
				skipped++
			case "q":
				return accepted, rejected, skipped, true
			default:
				continue
			}
			break
		}
		fmt.Println()
	}
	return accepted, rejected, skipped, false
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// pluralS is the plain -s plural, used well beyond this file (continuity.go).
// It lived here before the rollup half of this file existed and stays for the
// same reason: it is not rollup-specific, it just happened to be next to code
// that was.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
