package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/contextpack"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// ledger records one event for `logos usage`. A failure goes to stderr and
// does not fail the command: the pack or the check already reached the user,
// and a total that stopped growing must say why rather than look complete.
func ledger(vault string, e usagepkg.Event) {
	if err := usagepkg.Record(vault, e); err != nil {
		fmt.Fprintf(os.Stderr, "· could not add this to the usage ledger, so `logos usage` will not count it: %v\n", err)
	}
}

func ledgerPack(vault, via, project string, pack contextpack.Pack) {
	if err := usagepkg.RecordPack(vault, via, project, pack.Budget); err != nil {
		fmt.Fprintf(os.Stderr, "· could not add this to the usage ledger, so `logos usage` will not count it: %v\n", err)
	}
}

// runUsage prints what Logos has measurably done for this vault.
//
// Every line is a receipt with its method beside it, not in a footnote a flag
// hides: the token figure is a pack against its own candidates, which is all
// Logos can see, and a reader who takes it for their API bill has been misled
// by a number that was true.
//
//	logos usage [project] [--usd RATE]
func runUsage(args []string) error {
	if len(args) == 1 && (args[0] == "off" || args[0] == "on") {
		return setUsageRecording(args[0] == "on")
	}
	var project, rate string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--usd":
			if i+1 < len(args) {
				i++
				rate = args[i]
			}
		default:
			if project == "" {
				project = args[i]
			}
		}
	}
	usd := 0.0
	if hasFlag(args, "--usd") {
		v, err := strconv.ParseFloat(strings.TrimPrefix(rate, "$"), 64)
		if err != nil || v <= 0 {
			return fmt.Errorf("--usd needs a price in dollars per million tokens, such as --usd 3, not %q", rate)
		}
		usd = v
	}

	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}
	events, bad, err := usagepkg.Read(v)
	if err != nil {
		return err
	}
	t := usagepkg.Sum(events, project)
	if !usagepkg.Recording(v) {
		// Totals that stopped growing read as Logos doing nothing; say why.
		fmt.Println("· recording is off — the totals below stopped when it was turned off. `logos usage on` resumes it.")
		fmt.Println()
	}

	who := "every project"
	if project != "" {
		who = project
	}
	if t.Packs == 0 && t.DeadEndChecks == 0 {
		fmt.Printf("· nothing recorded for %s yet — %s fills as context packs are sent\n", who, filepath.Join(v, usagepkg.Dir))
		fmt.Println("  (resume, context) and recorded dead ends are handed back (before_you_try, tried, why)")
		printUnreadable(bad, v)
		return nil
	}
	fmt.Printf("logos usage — %s, since %s\n\n", who, time.Unix(t.Since, 0).Format("2 Jan 2006"))

	they := "they"
	if t.Packs == 1 {
		they = "it"
	}
	fmt.Printf("· %d context pack%s sent ~%s tokens; uncut, what %s drew from came to ~%s — ~%s left out\n",
		t.Packs, pluralS(t.Packs), thousands(t.Sent), they, thousands(t.Full), thousands(t.Saved()))
	fmt.Println("  (each pack against its own candidates, at ~4 characters a token — not your API usage)")
	if usd > 0 {
		fmt.Printf("  ≈ $%.2f at $%g per million tokens, the rate you gave\n", float64(t.Saved())*usd/1e6, usd)
	}

	fmt.Printf("· %d check%s handed back %d recorded dead end%s before a retry\n",
		t.DeadEndChecks, pluralS(t.DeadEndChecks), t.Rulings, pluralS(t.Rulings))
	fmt.Println("  (before_you_try, tried and why — dead ends returned, not mistakes proven avoided)")

	fmt.Println("· handoffs: not counted — the alternative is a summary written or pasted by hand, and Logos")
	fmt.Println("  never sees how long that would have been; a guess is not shown as a number")
	printUnreadable(bad, v)
	return nil
}

func printUnreadable(bad int, v string) {
	if bad > 0 {
		fmt.Printf("· %d unreadable line%s in %s skipped — the totals are missing them\n",
			bad, pluralS(bad), filepath.Join(v, usagepkg.Dir))
	}
}

// thousands writes 61234 as 61,234, since a token count is read at a glance.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func setUsageRecording(on bool) error {
	v := vaultPath()
	if _, err := os.Stat(v); err != nil {
		return missingVaultError(v)
	}
	if err := usagepkg.SetRecording(v, on); err != nil {
		return err
	}
	if on {
		fmt.Printf("usage recording is on for %s — packs sent and dead ends handed back are counted in %s\n", v, filepath.Join(v, usagepkg.Dir))
		return nil
	}
	fmt.Printf("usage recording is off for %s — nothing new is counted; what is already in %s is left alone\n", v, filepath.Join(v, usagepkg.Dir))
	fmt.Println("  `logos usage on` resumes it. LOGOS_USAGE=off turns it off for one process.")
	return nil
}
