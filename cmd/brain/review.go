package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
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

// runReview is the one review command for everything waiting on a human:
// rollup's proposed vault notes and Stage 4's quarantined memories. Two
// different queues, two different payloads, but the same question each
// time — accept, reject, or skip — so they share one command and one final
// tally instead of asking the user to remember two commands for "things I
// haven't looked at yet".
func runReview(all bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := rollup.InitQueue(ix.DB); err != nil {
		return err
	}
	if err := memory.Init(ix.DB); err != nil {
		return err
	}

	notes, err := rollup.List(ix.DB, rollup.Pending)
	if err != nil {
		return err
	}
	memories, err := memory.Pending(ix.DB)
	if err != nil {
		return err
	}
	if len(notes) == 0 && len(memories) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	var accepted, rejected, skipped int

	if len(notes) > 0 {
		a, r, s, quit := reviewNotes(ix, in, notes, all)
		accepted, rejected, skipped = accepted+a, rejected+r, skipped+s
		if quit {
			fmt.Printf("\n%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
			return nil
		}
	}
	if len(memories) > 0 {
		a, r, s, quit := reviewMemories(ix, in, memories, all)
		accepted, rejected, skipped = accepted+a, rejected+r, skipped+s
		if quit {
			fmt.Printf("\n%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
			return nil
		}
	}

	fmt.Printf("%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
	if accepted > 0 {
		fmt.Println("run `brain index` to pick up the new notes")
	}
	return nil
}

// reviewNotes runs the accept/reject/skip loop over rollup's proposed vault
// notes. quit reports whether the user asked to stop early (q), in which
// case runReview must not go on to the memory queue behind their back.
func reviewNotes(ix *index.Index, in *bufio.Reader, pending []rollup.Proposal, all bool) (accepted, rejected, skipped int, quit bool) {
	fmt.Printf("%d pending note%s · [a]ccept  [r]eject  [s]kip  [e]vidence  [q]uit\n\n", len(pending), pluralS(len(pending)))

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
				return accepted, rejected, skipped, true
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

// reviewMemories runs the same accept/reject/skip loop over Stage 4's
// quarantined memories — the queue that MCP's remember tool fills so that no
// agent writes straight into the vault (see internal/mcpserver's
// quarantineMCP and internal/memory/quarantine.go). Accept releases the
// memory into active memory and flushes it to the vault; reject discards it
// for good. There is no "evidence" view here the way rollup has one — a
// memory is already the atomic fact, not a summary of events behind it.
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

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
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
