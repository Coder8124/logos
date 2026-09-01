package main

import (
	"fmt"
	"time"

	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/secretary"
)

// runBrief is the secretary speaking first. Unlike ask/search/timeline, you
// give it nothing — it tells you what it thinks you should know right now.
func runBrief() error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := secretary.Init(ix.DB); err != nil {
		return err
	}
	if err := rollup.InitQueue(ix.DB); err != nil {
		return err
	}

	b, err := secretary.Compose(ix.DB, time.Now())
	if err != nil {
		return err
	}
	b.Review, _ = rollup.PendingCount(ix.DB)

	fmt.Printf("%s.\n", b.Greeting)

	if b.IsQuiet() && b.Review == 0 {
		fmt.Println("Nothing pressing — you're clear.")
		return nil
	}

	if len(b.Upcoming) > 0 {
		fmt.Println("\nComing up")
		for _, m := range b.Upcoming {
			when := fmt.Sprintf("in %dm", m.InMin)
			if m.InMin >= 90 {
				when = "at " + m.At
			}
			mark := " "
			if m.Imminent {
				mark = "!" // leave now
			}
			cal := ""
			if m.Cal != "" {
				cal = "  [" + m.Cal + "]"
			}
			fmt.Printf("  %s %-8s %s%s\n", mark, when, m.Title, cal)
		}
	}

	if len(b.Loops) > 0 {
		fmt.Println("\nOpen loops")
		for _, l := range b.Loops {
			mark := " "
			if l.Stale {
				mark = "!" // the ones most likely to have slipped
			}
			age := fmt.Sprintf("%dd", l.AgeDays)
			who := ""
			if l.Who != "" {
				who = "  → " + l.Who
			}
			due := ""
			if l.Due != "" {
				due = "  (" + l.Due + ")"
			}
			fmt.Printf("  %s %-4s %s%s%s\n", mark, age, l.Text, who, due)
		}
	}

	if len(b.Dormant) > 0 {
		fmt.Println("\nGone quiet")
		for _, n := range b.Dormant {
			fmt.Printf("  · %s — %s\n", n.Text, n.Detail)
		}
	}

	if len(b.Usual) > 0 {
		fmt.Println("\nAround now, you usually")
		for _, n := range b.Usual {
			fmt.Printf("  · %s (%s)\n", n.Text, n.Detail)
		}
	}

	if len(b.Remembers) > 0 {
		fmt.Println("\nKeeping in mind")
		for _, m := range b.Remembers {
			fmt.Printf("  · %s\n", m)
		}
	}

	if b.Review > 0 {
		fmt.Printf("\n%d proposal(s) waiting — `brain review`\n", b.Review)
	}
	return nil
}

// commitmentCmd manages open loops by hand, for the things the extractor misses
// or the user wants to add directly.
func commitmentCmd(args []string) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := secretary.Init(ix.DB); err != nil {
		return err
	}

	if len(args) == 0 {
		open, err := secretary.Open_(ix.DB)
		if err != nil {
			return err
		}
		if len(open) == 0 {
			fmt.Println("no open loops")
			return nil
		}
		for _, c := range open {
			who := ""
			if c.Who != "" {
				who = " → " + c.Who
			}
			fmt.Printf("  [%d] %s%s\n", c.ID, c.Text, who)
		}
		return nil
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: brain loop add <text>")
		}
		c := &secretary.Commitment{Text: joinArgs(args[1:])}
		if _, err := secretary.Add(ix.DB, c); err != nil {
			return err
		}
		fmt.Println("tracked")
	case "done":
		id := parseID(args)
		return secretary.SetStatus(ix.DB, id, secretary.Done)
	case "drop":
		id := parseID(args)
		return secretary.SetStatus(ix.DB, id, secretary.Dropped)
	default:
		return fmt.Errorf("usage: brain loop [add <text> | done <id> | drop <id>]")
	}
	return nil
}
