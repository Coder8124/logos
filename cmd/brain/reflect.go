package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/pragun/brain/internal/reflection"
)

// runReflect prints descriptive statistics over the memory — what it knows, how
// sure it is, how it's grown, what it leans on, and what's lingered. All computed,
// no model: the numeric floor beneath the interpretive mirror.
func runReflect() error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	r, err := reflection.Compose(ix.DB, time.Now())
	if err != nil {
		return err
	}

	if r.Total == 0 {
		fmt.Println("nothing to reflect on yet — the assistant learns as you talk to it.")
		return nil
	}

	fmt.Println("Reflection")

	fmt.Printf("\n  What it knows\n")
	fmt.Printf("    %d memories · avg confidence %.2f\n", r.Total, r.AvgConfidence)
	if parts := countLine(r.ByKind); parts != "" {
		fmt.Printf("    %s\n", parts)
	}
	fmt.Printf("    %d sure (≥0.8) · %d hunches (<0.6)\n", r.HighConfidence, r.Hypotheses)

	if len(r.ByProject) > 0 {
		fmt.Printf("\n  By project\n    %s\n", countLine(r.ByProject))
	}

	fmt.Printf("\n  Growth — memories learned / week (last %d)\n", len(r.Growth))
	fmt.Printf("    %s  (%d total)\n", sparkline(r.Growth), growthTotal(r.Growth))

	if len(r.MostExercised) > 0 {
		fmt.Printf("\n  Leans on most\n")
		for _, m := range r.MostExercised {
			fmt.Printf("    %2d×  (%s) %s\n", m.Uses, m.Kind, truncateLine(m.Text, 56))
		}
	}

	if len(r.LingeringLoops) > 0 {
		fmt.Printf("\n  Lingering commitments\n")
		now := time.Now()
		for _, c := range r.LingeringLoops {
			days := int(now.Sub(time.Unix(c.Created, 0)).Hours() / 24)
			fmt.Printf("    %3dd  %s\n", days, truncateLine(c.Text, 56))
		}
	}
	return nil
}

func countLine(cs []reflection.Count) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s %d", c.Label, c.N))
	}
	return strings.Join(parts, " · ")
}

func growthTotal(ws []reflection.WeekCount) int {
	t := 0
	for _, w := range ws {
		t += w.Learned
	}
	return t
}

// sparkline renders the weekly counts as block characters, scaled to the busiest
// week — a glance at the shape of learning over time.
func sparkline(ws []reflection.WeekCount) string {
	if len(ws) == 0 {
		return ""
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	max := 0
	for _, w := range ws {
		if w.Learned > max {
			max = w.Learned
		}
	}
	var b strings.Builder
	for _, w := range ws {
		if max == 0 {
			b.WriteRune(blocks[0])
			continue
		}
		idx := (w.Learned * (len(blocks) - 1)) / max
		b.WriteRune(blocks[idx])
	}
	return b.String()
}
