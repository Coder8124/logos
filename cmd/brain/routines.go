package main

import (
	"fmt"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/routine"
)

// runRoutines mines the episodic tier for recurring structure.
//
// Listing is read-only and free; --propose is what puts anything in the review
// queue. Keeping those separate means the user can look at what was found
// before deciding any of it deserves to be a note.
func runRoutines(days int, propose bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	now := time.Now()
	from := now.AddDate(0, 0, -days).Unix()

	events, err := capture.Range(ix.DB, from, now.Unix()+1)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Printf("no events in the last %dd — run `brain capture --daemon` for a while first\n", days)
		return nil
	}

	periodics := routine.FindPeriodic(events)
	// Browser history reaches back months where focus events start the day the
	// daemon first ran, so mine both and label which is which.
	sites := routine.FindPeriodicSites(events)
	sequences := routine.FindSequences(events)
	anomalies := routine.FindAnomalies(events, now.Unix())

	fmt.Printf("· %d events over %dd\n\n", len(events), days)

	if len(periodics) == 0 && len(sites) == 0 && len(sequences) == 0 && len(anomalies) == 0 {
		fmt.Printf("nothing clears the support threshold yet (needs %d occurrences across %d weeks)\n",
			routine.MinOccurrences, routine.MinWeeks)
		return nil
	}

	if len(periodics) > 0 {
		fmt.Println("─── time of day ───")
		for _, p := range periodics {
			fmt.Printf("  %-20s %-9s %s   %d× over %dw, %.0f%% of days\n",
				p.App, p.Cadence(), p.Window(), p.Occurrences, p.Weeks, p.Consistency*100)
		}
		fmt.Println()
	}

	if len(sites) > 0 {
		fmt.Println("─── sites by time of day ───")
		for i, p := range sites {
			if i >= 12 {
				fmt.Printf("  … %d more\n", len(sites)-i)
				break
			}
			fmt.Printf("  %-30s %-9s %s   %d× over %dw, %.0f%% of days\n",
				p.App, p.Cadence(), p.Window(), p.Occurrences, p.Weeks, p.Consistency*100)
		}
		fmt.Println()
	}

	if len(sequences) > 0 {
		fmt.Println("─── transitions ───")
		for _, s := range sequences {
			fmt.Printf("  %-40s %d×, %.0f%% of the time\n", s.String(), s.Count, s.Share*100)
		}
		fmt.Println()
	}

	if len(anomalies) > 0 {
		fmt.Println("─── unusual ───")
		for _, a := range anomalies {
			fmt.Printf("  %s\n", a.String())
		}
		fmt.Println()
	}

	if !propose {
		if len(periodics)+len(sites)+len(sequences) > 0 {
			fmt.Println("run with --propose to queue these as routine notes")
		}
		return nil
	}

	if err := rollup.InitQueue(ix.DB); err != nil {
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

	n, err := rollup.ProposeRoutines(ix.DB, rt, append(periodics, sites...), sequences)
	if err != nil {
		return err
	}
	fmt.Printf("%d routines queued · run `brain review`\n", n)
	return nil
}
