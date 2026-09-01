package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/router"
)

// The trust loop. The system proposes, the user accepts. Everything the model
// infers passes through here before it touches the vault.

func runRollup(dateArg string, dryRun bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := rollup.InitQueue(ix.DB); err != nil {
		return err
	}

	date := time.Now()
	if dateArg != "" {
		date, err = time.ParseInLocation("2006-01-02", dateArg, time.Local)
		if err != nil {
			return fmt.Errorf("bad date %q, want YYYY-MM-DD", dateArg)
		}
	}

	cfg, err := router.Load(ix.Vault)
	if err != nil {
		return err
	}
	rt, err := router.New(cfg, ix.Vault)
	if err != nil {
		return err
	}

	fmt.Printf("· rolling up %s\n", date.Format("2006-01-02"))
	res, err := rollup.Day(ix.DB, ix.Vault, rt, date, dryRun)
	if err != nil {
		return err
	}

	if res.Sessions == 0 {
		fmt.Println("no activity recorded for that day")
		return nil
	}

	fmt.Printf("%d sessions", res.Sessions)
	if res.Skipped > 0 {
		fmt.Printf(" (%d idle, skipped)", res.Skipped)
	}
	if dryRun {
		fmt.Printf(" · %d proposals would be queued · nothing written\n", res.Proposals)
		return nil
	}
	fmt.Printf(" · wrote %s · %d proposals queued\n", res.DailyPath, res.Proposals)
	fmt.Println("run `brain review` to accept or reject them")
	return nil
}

func runReview(all bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := rollup.InitQueue(ix.DB); err != nil {
		return err
	}

	pending, err := rollup.List(ix.DB, rollup.Pending)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("note queue is empty")
		return nil
	}

	fmt.Printf("%d pending · [a]ccept  [r]eject  [s]kip  [e]vidence  [q]uit\n\n", len(pending))
	in := bufio.NewReader(os.Stdin)

	accepted, rejected, skipped := 0, 0, 0

	for i, p := range pending {
		if !all && i >= 20 {
			fmt.Printf("\n… %d more, run again to continue\n", len(pending)-i)
			break
		}

		fmt.Printf("[%d/%d] %s\n", i+1, len(pending), p.Summary())
		fmt.Printf("        conf %.2f · %s · %d observations\n", p.Conf, p.Model, len(p.Evidence))

		for {
			fmt.Print("      > ")
			line, err := in.ReadString('\n')
			if err != nil {
				fmt.Println()
				return nil
			}

			switch strings.TrimSpace(strings.ToLower(line)) {
			case "a", "y":
				if err := rollup.Apply(ix.DB, ix.Vault, p); err != nil {
					fmt.Printf("      ! %v\n", err)
					// A proposal that cannot be applied is not accepted;
					// leaving it pending is better than lying about it.
					break
				}
				rollup.SetStatus(ix.DB, p.ID, rollup.Accepted)
				accepted++
				fmt.Println("      ✓ applied")
			case "r", "n":
				rollup.SetStatus(ix.DB, p.ID, rollup.Rejected)
				rejected++
				fmt.Println("      ✗ rejected")
			case "s", "":
				skipped++
			case "e":
				printEvidence(ix, p)
				continue // stay on this proposal
			case "q":
				fmt.Printf("\n%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
				return nil
			default:
				continue
			}
			break
		}
		fmt.Println()
	}

	fmt.Printf("%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
	if accepted > 0 {
		fmt.Println("run `brain index` to pick up the new notes")
	}
	return nil
}

// printEvidence is the answer to "why does it think that". Every proposal can
// be traced back to the rows behind it; without this the queue is just a
// model's word.
func printEvidence(ix *index.Index, p rollup.Proposal) {
	events, err := capture.ByIDs(ix.DB, p.Evidence)
	if err != nil || len(events) == 0 {
		fmt.Println("      (evidence rows no longer available — likely pruned)")
		return
	}

	shown := events
	if len(shown) > 12 {
		shown = shown[:12]
	}
	for _, e := range shown {
		switch e.Kind {
		case capture.URL:
			fmt.Printf("        %s  %s\n", capture.HHMM(e.TS), e.URL)
		case capture.Commit:
			fmt.Printf("        %s  commit %s — %s\n", capture.HHMM(e.TS), e.Path, e.Title)
		default:
			fmt.Printf("        %s  %s %s\n", capture.HHMM(e.TS), e.App, e.Title)
		}
	}
	if len(events) > len(shown) {
		fmt.Printf("        … %d more\n", len(events)-len(shown))
	}
}
