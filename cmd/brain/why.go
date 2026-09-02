package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// brain why <file> — what was being decided when this file was last worked on.
//
// The complement to git blame, which answers who and when and structurally
// cannot answer why. The reasoning lives in checkpoints, next to the list of
// files each one touched, and until now nothing joined the two.
//
// Needs no model and no index: it reads markdown out of the vault.

func runWhy(args []string) error {
	var path string
	limit := 5
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "-n":
			if i+1 < len(args) {
				i++
				limit = atoiOr(args[i], limit)
			}
		default:
			if path == "" {
				path = args[i]
			}
		}
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("usage: brain why <file> [--limit N]")
	}

	vault := vaultPath()
	mentions, err := session.Touching(vault, path, limit)
	if err != nil {
		return err
	}
	if len(mentions) == 0 {
		// Say which of the two possible nothings this is. "No checkpoint
		// mentions it" is a fact about the record; "brain does not know" would
		// imply the file is uninteresting, which is a different claim.
		fmt.Printf("No checkpoint mentions %s.\n", path)
		fmt.Println("\nEither nothing was written down while working on it, or it was")
		fmt.Println("recorded under a different path. `brain sessions <project>` lists what")
		fmt.Println("has been checkpointed.")
		return nil
	}

	fmt.Printf("%s — %d checkpoint(s)\n", path, len(mentions))

	for _, m := range mentions {
		when := "unknown date"
		if m.TS > 0 {
			t := time.Unix(m.TS, 0)
			when = t.Format("2 Jan 2006")
			if age := time.Since(t); age < 48*time.Hour {
				when = "recently"
			}
		}
		who := m.Agent
		if who == "" {
			who = "unknown"
		}

		fmt.Printf("\n─── %s · %s", when, who)
		if m.Project != "" {
			fmt.Printf(" · %s", m.Project)
		}
		fmt.Println()
		if m.Matched != "" && normaliseForDisplay(m.Matched) != normaliseForDisplay(path) {
			// The checkpoint recorded a different spelling. Show it, so a wrong
			// match is visible as a wrong match rather than as a wrong decision.
			fmt.Printf("    recorded as: %s\n", m.Matched)
		}
		if m.Task != "" {
			fmt.Printf("    while: %s\n", m.Task)
		}

		// Dead ends first. They are the expensive half and the reason someone is
		// asking — a decision explains the shape of the code, a ruled-out
		// approach explains why it is not some other shape.
		bullets("ruled out", m.Failed)
		bullets("decided", m.Decisions)
		bullets("still open", m.Questions)

		fmt.Printf("    → %s\n", m.Slug)
	}

	fmt.Println("\nThis is what was written down while the file was touched, not an")
	fmt.Println("explanation of the code. A decision nobody recorded is not here.")
	return nil
}

func bullets(label string, items []string) {
	for i, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		head := "  " + label + ":"
		if i > 0 {
			head = "  " + strings.Repeat(" ", len(label)) + " "
		}
		fmt.Printf("  %-13s %s\n", head, it)
	}
}

func normaliseForDisplay(p string) string {
	return strings.TrimPrefix(strings.TrimSpace(p), "./")
}

func atoiOr(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
