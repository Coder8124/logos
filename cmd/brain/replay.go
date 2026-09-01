package main

import (
	"fmt"
	"time"

	"github.com/Coder8124/brain/internal/replay"
)

// runReplay prints "since you've been away" — the catch-up the app should open
// with after a gap, instead of a blank prompt. By default it advances the
// last-seen marker (you've now caught up); --peek looks without resetting it.
func runReplay(peek bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	now := time.Now()
	res, err := replay.Compose(ix.DB, now)
	if err != nil {
		return err
	}
	printReplay(res)

	if !peek {
		if err := replay.Mark(ix.DB, res.Until); err != nil {
			return err
		}
	}
	return nil
}

func printReplay(res replay.Result) {
	if res.FirstRun {
		fmt.Printf("Welcome back. First catch-up, so here's the last %s.\n", humanizeAway(res.Away()))
	} else {
		fmt.Printf("Since you've been away (%s)\n", humanizeAway(res.Away()))
	}

	if res.Empty() {
		fmt.Println("\n  Nothing changed while you were gone.")
		return
	}

	// Memory: a one-line tally, then a few of the freshly learned facts.
	if n := len(res.Learned) + len(res.Dropped) + len(res.Corroborated); n > 0 {
		fmt.Printf("\nMemory\n")
		fmt.Printf("  + %d learned   - %d dropped   ~ %d corroborated\n",
			len(res.Learned), len(res.Dropped), len(res.Corroborated))
		for i, e := range res.Learned {
			if i >= 5 {
				fmt.Printf("    … and %d more\n", len(res.Learned)-5)
				break
			}
			fmt.Printf("    + %s\n", truncateLine(e.Text, 64))
		}
	}

	if len(res.Projects) > 0 {
		fmt.Printf("\nProjects that moved\n")
		for i, p := range res.Projects {
			if i >= 6 {
				break
			}
			fmt.Printf("  • %s — %s\n", p.Name, humanizeAgo(p.LastActive))
		}
	}

	if len(res.ClosedLoops) > 0 || res.OpenLoops > 0 {
		fmt.Printf("\nLoops\n")
		if len(res.ClosedLoops) > 0 {
			fmt.Printf("  ✓ closed %d\n", len(res.ClosedLoops))
			for i, c := range res.ClosedLoops {
				if i >= 3 {
					break
				}
				fmt.Printf("    ✓ %s\n", truncateLine(c.Text, 60))
			}
		}
		if res.OpenLoops > 0 {
			fmt.Printf("  ○ %d still open\n", res.OpenLoops)
		}
	}

	if res.Insights > 0 {
		fmt.Printf("\nOvernight\n")
		fmt.Printf("  %d dreamed connection(s) waiting — `brain dream review`\n", res.Insights)
	}
}

// humanizeAway renders a gap as a coarse, friendly span.
func humanizeAway(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "hour"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d < 48*time.Hour:
		return "day"
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d weeks", int(d.Hours()/(24*7)))
	}
}

func humanizeAgo(ts int64) string {
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
