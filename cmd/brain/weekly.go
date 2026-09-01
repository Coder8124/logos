package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Coder8124/brain/internal/secretary"
)

// runWeekly prints the Sunday executive-assistant briefing: what got done, what
// is still open, who you dealt with, what is due, where your time went, the
// habits underneath, and what to do about it — all computed from captured data.
func runWeekly(asJSON bool) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := secretary.Init(ix.DB); err != nil {
		return err
	}

	r, err := secretary.Review(ix.DB, time.Now())
	if err != nil {
		return err
	}
	if asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("Week in review · %s – %s\n",
		r.Start.Format("Jan 2"), r.End.Format("Jan 2"))
	fmt.Printf("%s\n", r.Headline())

	block("Accomplished", len(r.Accomplished))
	for _, it := range topItems(r.Accomplished, 8) {
		fmt.Printf("  ✓ %-52s %s\n", truncateLine(it.Text, 52), dim(it.Detail))
	}

	block("Still open", len(r.Unfinished))
	for _, it := range topItems(r.Unfinished, 8) {
		fmt.Printf("  ○ %-52s %s\n", truncateLine(it.Text, 52), dim(it.Detail))
	}

	block("People", len(r.People))
	for _, p := range r.People {
		fmt.Printf("  • %-24s %d× %s\n", p.Name, p.Count, dim("via "+p.Via))
	}

	block("Deadlines", len(r.Deadlines))
	for i, d := range r.Deadlines {
		if i >= 8 {
			break
		}
		fmt.Printf("  ⏳ %-40s %s\n", truncateLine(d.Text, 40), d.When)
	}

	block("Where your time went", len(r.Topics))
	for _, tp := range r.Topics {
		fmt.Printf("  %5.1fh  %-28s %s\n", tp.Hours, truncateLine(tp.Label, 28), dim(tp.Detail))
	}

	block("Habits", len(r.Habits))
	for _, h := range r.Habits {
		fmt.Printf("  ↻ %s\n", h)
	}

	block("Recommendations", len(r.Recommendations))
	for _, rec := range r.Recommendations {
		fmt.Printf("  → %s\n", rec)
	}
	return nil
}

func block(title string, n int) {
	fmt.Printf("\n%s", title)
	if n == 0 {
		fmt.Print("\n  —\n")
		return
	}
	fmt.Println()
}

func topItems(items []secretary.ReviewItem, n int) []secretary.ReviewItem {
	if len(items) > n {
		return items[:n]
	}
	return items
}

func dim(s string) string {
	if s == "" {
		return ""
	}
	return "\x1b[2m" + s + "\x1b[0m"
}
