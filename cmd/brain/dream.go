package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/Coder8124/brain/internal/dream"
	"github.com/Coder8124/brain/internal/router"
)

// dreamHour is the local hour past which the daemon runs the nightly pass. Zero
// is midnight; the day just ended, so it is the natural moment to sleep on it.
const dreamHour = 0

// dreamCmd is the nightly consolidation pass and its review queue.
//
//	brain dream [--date YYYY-MM-DD] [--phase nrem|rem] [--dry-run]
//	brain dream review
//	brain dream accept|reject <id>
func dreamCmd(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "review":
			return dreamReview()
		case "accept":
			return dreamDecide(args[1:], true)
		case "reject":
			return dreamDecide(args[1:], false)
		}
	}
	return runDream(flagStr(args, "--date", ""), flagStr(args, "--phase", "all"), hasFlag(args, "--dry-run"))
}

func runDream(dateArg, phase string, dryRun bool) error {
	switch phase {
	case "", "all", "nrem", "rem":
	default:
		return fmt.Errorf("bad --phase %q, want nrem, rem, or all", phase)
	}
	if phase == "" {
		phase = "all"
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

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

	if dryRun {
		fmt.Printf("· dreaming on %s (dry run — nothing will be written)\n", date.Format("2006-01-02"))
	} else {
		fmt.Printf("· dreaming on %s\n", date.Format("2006-01-02"))
	}

	res, err := dream.Run(ix.DB, ix.Vault, rt, defaultEmbedModel, date, phase, dryRun)
	if err != nil {
		return err
	}
	printDream(res, phase)
	return nil
}

func printDream(res dream.Result, phase string) {
	if phase == "all" || phase == "nrem" {
		fmt.Println("\nNREM — stabilise")
		if res.ReplaySkipped {
			fmt.Println("  replay:     skipped — no model available to judge duplicates")
		} else {
			fmt.Printf("  replay:     %d consolidated (%d merged, %d superseded)\n", res.Replayed, res.Merged, res.Superseded)
		}
		fmt.Printf("  gist:       %d standing fact(s) learned\n", res.Gists)
		fmt.Printf("  downscale:  %d memories renormalised\n", res.Downscaled)
		fmt.Printf("  artifacts:  %d tied to their work\n", res.Linked)
	}
	if phase == "all" || phase == "rem" {
		fmt.Println("\nREM — recombine")
		if res.REMSkipped {
			fmt.Println("  skipped — no reasoning model (T2) available")
		} else if res.Insights == 0 {
			fmt.Println("  no new connections tonight")
		} else {
			fmt.Printf("  %d connection(s) proposed — `brain dream review` to see them\n", res.Insights)
		}
	}
}

// dreamNightly runs a full pass over the day that just ended, reusing an existing
// database handle. Called from the capture daemon so it shares the daemon's
// single connection rather than opening a second one against the same file.
func dreamNightly(db *sql.DB, vault string) error {
	cfg, err := router.Load(vault)
	if err != nil {
		return err
	}
	rt, err := router.New(cfg, vault)
	if err != nil {
		return err
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	fmt.Printf("· dreaming on %s\n", yesterday.Format("2006-01-02"))
	res, err := dream.Run(db, vault, rt, defaultEmbedModel, yesterday, dream.PhaseAll, false)
	if err != nil {
		return err
	}
	if res.Insights > 0 {
		fmt.Printf("· %d overnight insight(s) — `brain dream review`\n", res.Insights)
	}
	return nil
}

func dreamReview() error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := dream.InitQueue(ix.DB); err != nil {
		return err
	}

	ins, err := dream.List(ix.DB, dream.Pending)
	if err != nil {
		return err
	}
	if len(ins) == 0 {
		fmt.Println("no dreamed insights waiting. run `brain dream` after a day's activity.")
		return nil
	}

	for _, in := range ins {
		fmt.Printf("[%d] %s\n", in.ID, in.Text)
		fmt.Printf("      bridges: %s\n", memText(ix.DB, in.EndpointA))
		fmt.Printf("           ⇄  %s\n", memText(ix.DB, in.EndpointB))
	}
	fmt.Println("\n`brain dream accept <id>` to keep · `brain dream reject <id>` to discard")
	return nil
}

func dreamDecide(args []string, accept bool) error {
	verb := "reject"
	if accept {
		verb = "accept"
	}
	if len(args) == 0 {
		return fmt.Errorf("which insight? `brain dream %s <id>`", verb)
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("bad insight id %q", args[0])
	}

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := dream.InitQueue(ix.DB); err != nil {
		return err
	}

	if !accept {
		if err := dream.SetStatus(ix.DB, id, dream.Rejected); err != nil {
			return err
		}
		fmt.Printf("discarded insight %d\n", id)
		return nil
	}

	in, err := dream.Get(ix.DB, id)
	if err != nil {
		return err
	}
	cfg, err := router.Load(ix.Vault)
	if err != nil {
		return err
	}
	rt, err := router.New(cfg, ix.Vault)
	if err != nil {
		return err
	}
	if _, err := dream.Accept(ix.DB, rt.Local(), defaultEmbedModel, in); err != nil {
		return err
	}
	fmt.Printf("kept insight %d — stored as a low-confidence memory to be corroborated\n", id)
	return nil
}

// memText renders a memory by id for the review view, falling back to the id if
// the memory has since been superseded or forgotten.
func memText(db *sql.DB, id int64) string {
	var t string
	db.QueryRow("SELECT text FROM memories WHERE id = ?", id).Scan(&t)
	if t == "" {
		return fmt.Sprintf("memory %d", id)
	}
	return t
}
