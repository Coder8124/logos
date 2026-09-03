package main

import (
	"fmt"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// runContinuity answers the question `brain doctor` cannot: not "is brain
// itself working" but "is the handoff actually happening, project by
// project". A vault can have a perfectly healthy index and still be a place
// where nobody checkpoints — this is where that becomes visible instead of
// staying an impression nobody could check.
func runContinuity(args []string) error {
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		return err
	}

	report, err := session.AllContinuity(ix.DB, ix.Vault)
	if err != nil {
		return err
	}
	if len(report) == 0 {
		fmt.Println("no continuity yet — no checkpoints, no sessions, nothing recorded.")
		fmt.Println("run `brain note <project> ...` or `brain checkpoint <project> ...` to start one.")
		return nil
	}

	fmt.Println("─── continuity ───")
	quiet := 0
	for _, pc := range report {
		status := "ok"
		switch {
		case pc.LastCheckpoint == 0:
			status = "never checkpointed"
		case pc.Quiet():
			status = "quiet"
		}
		if status != "ok" {
			quiet++
		}
		fmt.Printf("  %-24s %s\n", pc.Project, status)
		if pc.LastCheckpoint != 0 {
			fmt.Printf("  %-24s   last checkpoint %s ago by %s · %d total\n",
				"", roughAge(pc.LastCheckpoint), orAgent(pc.LastAgent), pc.Checkpoints)
		}
		if pc.Uncommitted > 0 {
			fmt.Printf("  %-24s   %d uncommitted note(s)\n", "", pc.Uncommitted)
		}
		if pc.Abandoned > 0 {
			fmt.Printf("  %-24s   %d abandoned session(s) — see `brain sessions %s`\n", "", pc.Abandoned, pc.Project)
		}
	}
	fmt.Printf("\n%d project%s · %d quiet or never checkpointed\n",
		len(report), plural(len(report)), quiet)
	return nil
}

func orAgent(a string) string {
	if a == "" {
		return "an agent"
	}
	return a
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// roughAge renders a unix timestamp the way a person would say it. Mirrors
// internal/health's roughly() in spirit; kept as its own small copy here
// rather than exported, since the two packages have no other reason to share
// a dependency.
func roughAge(ts int64) string {
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
