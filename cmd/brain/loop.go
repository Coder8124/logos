package main

import (
	"database/sql"
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

	// `list` is a synonym for the bare form, not a subcommand of its own. Every
	// other listing verb here takes it, the help line named only the three verbs
	// that change something, and `brain loop list` answered with a usage error —
	// which reads as "there is no way to see them" rather than "you already are".
	if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
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
		return closeLoop(ix.DB, args, secretary.Done, "closed")
	case "drop":
		return closeLoop(ix.DB, args, secretary.Dropped, "dropped")
	default:
		return fmt.Errorf("usage: brain loop [list | add <text> | done <id> | drop <id>]")
	}
	return nil
}

// closeLoop resolves one loop and says which. The UPDATE behind SetStatus
// matches on id and is content to match nothing, so `brain loop done 9` on a
// vault whose loops are numbered 1 and 2 returned success and printed nothing —
// a mistyped id indistinguishable from a loop closed, with the one the user
// meant to close still open and nothing anywhere saying so.
func closeLoop(db *sql.DB, args []string, status secretary.Status, verb string) error {
	id := parseID(args)
	if id == 0 {
		return fmt.Errorf("usage: brain loop %s <id> — the id is the number in brackets in `brain loop`", args[0])
	}
	open, err := secretary.Open_(db)
	if err != nil {
		return err
	}
	for _, c := range open {
		if c.ID == id {
			if err := secretary.SetStatus(db, id, status); err != nil {
				return err
			}
			fmt.Printf("%s [%d] %s\n", verb, id, c.Text)
			return nil
		}
	}
	return fmt.Errorf("no open loop [%d] — run `brain loop` to see the ids", id)
}
