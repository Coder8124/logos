package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/textmatch"
)

// `logos review --stale` is where a drift warning gets an answer. Resume, show
// and before_you_try all say "recorded at X; f changed since, so it may no
// longer hold", and until this existed nothing could be done about it: the
// same warning came back on every resume until the reader learned to skip it,
// and then it was skipped on the day it was right.

// staleItem is one ruling or memory whose named files changed since it was
// recorded. Exactly one of entry (a checkpoint's Failed line, verbatim — the
// review file is keyed on it) or mem is set.
type staleItem struct {
	entry string
	mem   *memory.Memory
	drift gitstate.Drift
}

func (s staleItem) label() string {
	if s.mem != nil {
		// Said, because retiring excludes it everywhere: a global preference
		// that no longer fits this project may still fit every other one.
		if s.mem.Project == "" {
			return fmt.Sprintf("%s #%d, every project", s.mem.Kind, s.mem.ID)
		}
		return fmt.Sprintf("%s #%d", s.mem.Kind, s.mem.ID)
	}
	return "ruling"
}

func (s staleItem) text() string {
	if s.mem != nil {
		return s.mem.Text
	}
	return s.entry
}

// gatherStale collects what has drifted in the repository at dir: every
// ruling the scope's checkpoints hold, and every memory recorded at a commit
// of it — procedures among them, which no other listing shows.
//
// Rulings are read from the checkpoints directly rather than through
// deadend.Collect, whose Record is capped: the review file has to be keyed on
// the whole entry, or a long ruling retired here would still be handed over.
// They are measured the way deadend.MarkDrift measures them, newest
// checkpoint first so a ruling restated later is measured from the later
// commit, and from the commit a person re-confirmed it at when they did.
func gatherStale(vaultDir string, db *sql.DB, project, scope, dir string) ([]staleItem, error) {
	a := gitstate.NewAnchors(dir)
	if a == nil {
		return nil, nil
	}
	history, err := session.History(vaultDir, scope, 0)
	if err != nil {
		return nil, fmt.Errorf("reading the checkpoints: %w", err)
	}
	reviews, err := session.Reviews(vaultDir, scope)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", session.ReviewedFile, err)
	}

	var out []staleItem
	seen := map[string]bool{}
	for _, c := range history {
		for _, f := range c.Failed {
			k := session.ReviewKey(f)
			if seen[k] || session.IsPlaceholder(f) {
				continue
			}
			seen[k] = true
			retired, commit := reviews[k].For(c.TS, c.Git.Commit)
			if retired {
				continue
			}
			rec := deadend.ParseRecord(textmatch.Flatten(f))
			if d, ok := a.SinceAnchor(commit, rec.Anchor, rec.Raw); ok {
				out = append(out, staleItem{entry: f, drift: d})
			}
		}
	}

	mems, err := memory.AllInProject(db, project)
	if err != nil {
		return nil, fmt.Errorf("reading the memories: %w", err)
	}
	var kept []memory.Memory
	for _, m := range mems {
		// An excluded memory is one somebody already retired.
		if m.Pin != memory.PinNever {
			kept = append(kept, m)
		}
	}
	memory.MarkDrift(kept, dir)
	for i := range kept {
		if kept[i].Drift != nil {
			out = append(out, staleItem{mem: &kept[i], drift: *kept[i].Drift})
		}
	}
	return out, nil
}

// reviewStale asks keep, retire or re-confirm of each item. Every answer is
// written to the vault before the next question: a person who quits halfway
// keeps what they decided.
//
// Keep is the default and writes nothing — the warning stays, which is right
// for "I don't know yet". Retire leaves a ruling out of every later handoff
// and excludes a memory (on record still, `logos memory unpin` reverses it).
// Re-confirm moves the item's commit to head, so it is measured from now.
func reviewStale(in *bufio.Reader, items []staleItem, vaultDir string, db *sql.DB, scope, head string) (kept, retired, confirmed int, err error) {
	fmt.Printf("%d may be out of date · [k]eep  [r]etire  [c]onfirm still true at %s  [q]uit\n\n", len(items), short(head))
	for i, it := range items {
		fmt.Printf("[%d/%d] (%s) %s\n", i+1, len(items), it.label(), terminalSafe(textmatch.Flatten(it.text()), 400))
		fmt.Printf("        ⚠ %s\n", terminalSafe(it.drift.Note(), 300))
		for {
			fmt.Print("      > ")
			line, rerr := in.ReadString('\n')
			if rerr != nil {
				fmt.Println()
				return kept, retired, confirmed, nil
			}
			var act error
			switch strings.TrimSpace(strings.ToLower(line)) {
			case "k", "s", "":
				kept++
				fmt.Println("      · kept, still marked")
			case "r":
				if it.mem != nil {
					act = memory.Exclude(db, it.mem.ID)
				} else {
					act = session.Retire(vaultDir, scope, it.entry)
				}
				if act == nil {
					retired++
					fmt.Println("      ✗ retired — left out of later handoffs")
				}
			case "c":
				if it.mem != nil {
					act = memory.Reconfirm(db, it.mem.ID, head)
				} else {
					act = session.Reconfirm(vaultDir, scope, it.entry, head)
				}
				if act == nil {
					confirmed++
					fmt.Printf("      ✓ re-confirmed at %s\n", short(head))
				}
			case "q":
				return kept, retired, confirmed, nil
			default:
				continue
			}
			if act != nil {
				// Not a skip: the person answered and the vault refused it,
				// so the run stops rather than reporting a count that
				// includes an answer that was never kept.
				return kept, retired, confirmed, fmt.Errorf("recording the answer to %s: %w", it.label(), act)
			}
			break
		}
		fmt.Println()
	}
	return kept, retired, confirmed, nil
}

// runReviewStale is `logos review --stale`, for the project the user stands
// in: drift is measured against a working tree, and only this one is at hand.
func runReviewStale(ix *index.Index) error {
	project := projectHere()
	dir := dirFor(project)
	_, head := gitstate.Head(dir)
	if project == "" || head == "" {
		return fmt.Errorf("--stale measures what changed in a repository; run it inside the project's checkout")
	}
	// The scope resume reads: a linked worktree with checkpoints of its own
	// has its rulings, and its verdicts, filed under project/worktree, and
	// reviewing the project's instead would leave resume's warnings there
	// unanswerable.
	scope := project
	if w := gitstate.WorktreeName(dir); w != "" {
		if own, err := session.History(ix.Vault, project+"/"+w, 1); err == nil && len(own) > 0 {
			scope = project + "/" + w
		}
	}
	items, err := gatherStale(ix.Vault, ix.DB, project, scope, dir)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Printf("nothing recorded on %s names a file that changed since — no rulings or memories to review\n", project)
		return nil
	}
	kept, retired, confirmed, err := reviewStale(bufio.NewReader(os.Stdin), items, ix.Vault, ix.DB, scope, head)
	fmt.Printf("%d kept, %d retired, %d re-confirmed, %d not reached\n", kept, retired, confirmed, len(items)-kept-retired-confirmed)
	return err
}
