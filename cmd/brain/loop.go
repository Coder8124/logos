package main

import (
	"fmt"

	"github.com/Coder8124/brain/internal/secretary"
)

// commitmentCmd manages open loops by hand — "I'll send the deck", "waiting
// on the vendor" — the event-independent half of what used to be the
// secretary package. Its ambient half (an unprompted daily brief composed
// over captured calendar and activity events) was cut in 0.3.0 along with the
// rest of the ambient-capture tier; manual open-loop
// tracking has nothing to do with that and stays.
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
		added, err := secretary.Add(ix.DB, c)
		if err != nil {
			return err
		}
		// Add returns false when a fingerprint-equal loop is already on the
		// record: nothing was written to the cache or the vault. Printing
		// "tracked" for it made a no-op indistinguishable from a write, and the
		// user's second `brain loop` would show a list their new loop was not in
		// with nothing to explain why.
		if !added {
			fmt.Println("already tracked — an identical loop is already open")
			return nil
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
