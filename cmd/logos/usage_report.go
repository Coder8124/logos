package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// reportDays is the window `--month` reads: the last 30 days, today included.
const reportDays = 30

// usageReport prints the last month, a row to a day: checkpoints written,
// handoffs across agents and within one, and an estimate of the time they saved.
//
// Counts are receipts. The one figure that is not is the time, and it is the
// only line that says "estimate" and shows how it was made: Logos never sees
// how long a hand-written summary would have taken, so it prices the size of
// what a resume handed over at a typing speed the reader can change.
//
//	logos usage [project] --month [--each] [--wpm N]
func usageReport(v, project string, wpm int, each bool) error {
	events, bad, err := usagepkg.Read(v)
	if err != nil {
		return err
	}
	stamps, err := checkpointStamps(v)
	if err != nil {
		return err
	}
	now := time.Now()
	r := usagepkg.Build(events, stamps, now.AddDate(0, 0, -(reportDays-1)), now, project, wpm)

	who := "every project"
	if project != "" {
		who = project
	}
	if !usagepkg.Recording(v) {
		fmt.Println("· recording is off — restores and handoffs stopped being counted when it was turned off. `logos stats --usage on` resumes it.")
		fmt.Println()
	}
	fmt.Printf("logos stats --usage — %s, the last %d days (%s – %s)\n\n",
		who, reportDays, r.From.Format("2 Jan"), r.To.Format("2 Jan 2006"))

	t := r.Total
	if t.Checkpoints+t.Auto+t.Restores == 0 {
		fmt.Printf("· no checkpoints or restores for %s in the last %d days\n", who, reportDays)
		printSelfTests(r.SelfTests)
		printUnreadable(bad, v)
		return nil
	}

	fmt.Printf("· %d checkpoint%s written by an agent, %d of them left as a handoff to someone\n",
		t.Checkpoints, pluralS(t.Checkpoints), t.Declared)
	if t.Auto > 0 {
		fmt.Printf("  plus %d built automatically from the activity log when a session ended without one — what ran, not what was learned\n", t.Auto)
	}
	fmt.Printf("· %d restore%s — a resume that handed a checkpoint over\n", t.Restores, pluralS(t.Restores))
	fmt.Printf("  %d across agents: one agent's checkpoint resumed by a different one\n", t.Cross)
	fmt.Printf("  %d within one agent: its own earlier checkpoint, resumed in a later session\n", t.Same)
	if t.Unattributed > 0 {
		fmt.Printf("  %d not attributed: the writer or the reader is not named (a shell with no LOGOS_AGENT), so they are in neither count\n", t.Unattributed)
	}
	printSelfTests(r.SelfTests)

	fmt.Printf("· time saved, an ESTIMATE: ≈ %s across agents, ≈ %s within one agent\n",
		fmtMinutes(t.CrossMinutes), fmtMinutes(t.SameMinutes))
	fmt.Printf("  how: the size of each checkpoint handed over, typed out by hand at %d words a minute (--wpm changes it).\n", r.WPM)
	fmt.Println("  Logos cannot see how long a hand-written summary would have taken, so this prices the context's size and")
	fmt.Println("  nothing else — it leaves out a dead end that was not re-tried, which is the larger saving and cannot be timed.")

	printPairs("across agents", r.Cross)
	printPairs("within one agent", r.Same)

	fmt.Println()
	fmt.Printf("%-11s %11s %5s %9s %7s %8s\n", "day", "checkpoints", "auto", "handoffs", "within", "≈ saved")
	for _, d := range r.Days {
		saved := "-"
		if m := d.CrossMinutes + d.SameMinutes; m > 0 {
			saved = fmtMinutes(m)
		}
		fmt.Printf("%-11s %11d %5d %9d %7d %8s\n", d.Date, d.Checkpoints, d.Auto, d.Cross, d.Same, saved)
	}

	if each {
		fmt.Println()
		fmt.Printf("every checkpoint, oldest first (%d)\n", len(r.Each))
		for _, s := range r.Each {
			fmt.Printf("  %s  %-24s %-14s %s\n", time.Unix(s.TS, 0).Format("2006-01-02 15:04"), s.Project, nameOr(s.Agent), checkpointKind(s))
		}
	}
	printUnreadable(bad, v)
	return nil
}

func printSelfTests(n int) {
	if n > 0 {
		fmt.Printf("  %d more left out: `logos doctor` resuming its own test checkpoint, which is nobody's work handed over\n", n)
	}
}

func printPairs(label string, pairs []usagepkg.Pair) {
	if len(pairs) == 0 {
		return
	}
	fmt.Printf("\n%s\n", label)
	for _, p := range pairs {
		fmt.Printf("  %-14s → %-14s %4d  ≈ %s\n", p.From, p.Reader, p.Restores, fmtMinutes(p.Minutes))
	}
}

func checkpointKind(s usagepkg.CheckpointStamp) string {
	switch {
	case s.Auto:
		return "auto — built from the activity log"
	case strings.TrimSpace(s.HandoffTo) != "":
		return "handoff to " + strings.TrimSpace(s.HandoffTo)
	}
	return "checkpoint"
}

func nameOr(agent string) string {
	if agent == "" {
		return "(unnamed)"
	}
	return agent
}

// fmtMinutes writes 0.4 as "<1m", 38.2 as "38m" and 135 as "2h 15m".
func fmtMinutes(m float64) string {
	switch {
	case m <= 0:
		return "0m"
	case m < 1:
		return "<1m"
	case m < 60:
		return fmt.Sprintf("%dm", int(m+0.5))
	}
	total := int(m + 0.5)
	return fmt.Sprintf("%dh %02dm", total/60, total%60)
}

// checkpointStamps reads every checkpoint in the vault, in every scope: the
// ledger holds packs and dead ends, not checkpoints, so this is read from where
// they are written and covers checkpoints from before anyone asked for a report.
func checkpointStamps(v string) ([]usagepkg.CheckpointStamp, error) {
	scopes, err := session.Scopes(v)
	if err != nil {
		return nil, err
	}
	var out []usagepkg.CheckpointStamp
	for _, sc := range scopes {
		cps, err := session.History(v, sc, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "· could not read the checkpoints of %s: %v\n", sc, err)
			continue
		}
		for _, c := range cps {
			if c.TS == 0 {
				continue
			}
			out = append(out, usagepkg.CheckpointStamp{
				TS: c.TS, Project: sc, Agent: c.Agent, Auto: c.Auto, HandoffTo: c.HandoffTo,
			})
		}
	}
	return out, nil
}
