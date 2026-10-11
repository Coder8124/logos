package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/ops"
)

// The trust loop. Everything a model infers about you passes through here
// before it touches the vault.
//
// This file used to also run `logos rollup` and review its proposed vault
// notes, mined from ambient capture. That half was cut in 0.3.0 along with the
// rest of the ambient-capture tier. What remains —
// Stage 4's quarantined-memory review — is unrelated to it: it is the queue
// the `remember` MCP tool fills so no agent writes straight into the vault
// (internal/mcpserver's quarantine handling, internal/memory/quarantine.go),
// and this is the only place in the codebase that ever resolves it.

// runReview accepts or rejects memories waiting in quarantine.
const reviewUsage = `usage:
    logos review [--all]
    logos review --auto
    logos review --undo-auto [--since 24h|7d|YYYY-MM-DD]

Accept or reject memories agents saved, which wait in quarantine until you do.

    --all         go through every pending memory instead of the first 20
    --auto        list what the review switch accepted for you
    --undo-auto   return what the review switch accepted to the queue: since
                  your last review, or since --since
`

func runReview(args []string) error {
	// The review reads answers from stdin, so running it when someone only
	// asked for the flags blocks on the first pending memory.
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Print(reviewUsage)
		return nil
	}
	if err := checkFlags("logos review", args, flagSpec{
		valued: []string{"--since"},
		bare:   []string{"--all", "--auto", "--undo-auto"},
	}); err != nil {
		return err
	}
	if hasFlag(args, "--since") && !hasFlag(args, "--undo-auto") {
		return fmt.Errorf("--since goes with --undo-auto; nothing was done")
	}
	all := hasFlag(args, "--all")
	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()

	if err := memory.Init(ix.DB); err != nil {
		return err
	}
	switch {
	case hasFlag(args, "--auto"):
		return listAutoAccepted(ix.DB)
	case hasFlag(args, "--undo-auto"):
		return undoAutoAccepted(ix.DB, flagStr(args, "--since", ""))
	}
	// The review switch first, so what it would accept is not offered to the
	// person who turned it on to be spared exactly that. Said on the spot: this
	// is a command they are watching, and the log is for what nobody watched.
	if _, err := ops.AutoReview(ix.DB, ix.Vault, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "logos: the review switch %v\n", err)
	}
	// Before the queue is read: a proposal whose line the user deleted is one
	// they already rejected, and offering it again asks them twice.
	restored, rejected, err := memory.ReconcilePending(ix.DB)
	if err != nil {
		return err
	}
	announceAdoption(memory.PendingFile, "proposal", "rejected", restored, rejected)

	memories, err := memory.Pending(ix.DB)
	if err != nil {
		return err
	}
	if done, err := ops.AutoAcceptedSinceReview(ix.DB); err != nil {
		fmt.Fprintf(os.Stderr, "logos: could not count the memories accepted automatically: %v\n", err)
	} else if done > 0 {
		fmt.Printf("accepted %d automatically since your last review, %d waiting for you — logos review --auto lists them\n\n",
			done, len(memories))
	}
	if len(memories) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	accepted, rejected, skipped, _ := reviewMemories(ix, in, memories, all)

	// No "run `logos index`" after an accept: Accept writes the database and the
	// vault together, so the memory is recallable already.
	fmt.Printf("%d accepted, %d rejected, %d skipped\n", accepted, rejected, skipped)
	return nil
}

// reviewMemories runs the accept/reject/skip loop over Stage 4's
// quarantined memories. Accept releases the memory into active memory and
// flushes it to the vault; reject discards it for good. There is no
// "evidence" view here the way rollup's now-deleted proposal review had one —
// a memory is already the atomic fact, not a summary of events behind it.
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

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// pluralS is the plain -s plural, used well beyond this file (continuity.go).
// It lived here before the rollup half of this file existed and stays for the
// same reason: it is not rollup-specific, it just happened to be next to code
// that was.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// listAutoAccepted is what the review switch accepted, newest first, since a
// person last reviewed — and all of it when they have never reviewed since —
// with the one command that hands it back.
func listAutoAccepted(db *sql.DB) error {
	last, err := memory.LastReview(db)
	if err != nil {
		return err
	}
	ms, err := memory.AutoAccepted(db, last+1)
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		fmt.Println("nothing was accepted automatically since your last review")
		return nil
	}
	fmt.Printf("accepted automatically since your last review (%d):\n", len(ms))
	for _, m := range ms {
		fmt.Printf("  #%d  %s  (%s) %s\n", m.ID, time.Unix(m.AutoAccepted, 0).Local().Format("Mon 2 Jan 15:04"), m.Kind, m.Text)
	}
	fmt.Println("\nlogos review --undo-auto returns them to the queue")
	return nil
}

// undoAutoAccepted hands what the switch accepted back to a person. Each one
// is tried and a failure named: half an undo said as a whole one would leave
// memories the user believes are back in the queue still being recalled.
func undoAutoAccepted(db *sql.DB, sinceArg string) error {
	since, err := undoSince(db, sinceArg, time.Now())
	if err != nil {
		return err
	}
	ms, err := memory.AutoAccepted(db, since)
	if err != nil {
		return err
	}
	var undone int
	var failed []string
	for _, m := range ms {
		if err := memory.UndoAuto(db, m.ID); err != nil {
			failed = append(failed, fmt.Sprintf("#%d: %v", m.ID, err))
			continue
		}
		undone++
	}
	fmt.Printf("returned %d memor%s to the review queue — logos review\n", undone, pluralY(undone))
	if len(failed) > 0 {
		return fmt.Errorf("could not return %d: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

// undoSince reads --since as a duration back from now (24h, 7d) or a date;
// with none, it is the user's last review, which is what the queue line
// counted when it told them how many were accepted.
func undoSince(db *sql.DB, arg string, now time.Time) (int64, error) {
	if arg == "" {
		last, err := memory.LastReview(db)
		return last + 1, err
	}
	if d, ok := strings.CutSuffix(arg, "d"); ok {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			return now.AddDate(0, 0, -n).Unix(), nil
		}
	}
	if d, err := time.ParseDuration(arg); err == nil && d > 0 {
		return now.Add(-d).Unix(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", arg, time.Local); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("bad --since %q, want a duration like 24h or 7d, or YYYY-MM-DD; nothing was done", arg)
}
