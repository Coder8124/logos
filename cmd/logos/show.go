package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/ops"
	"github.com/Coder8124/logos/internal/session"
)

// How much of each list the page shows. A page that scrolls past the top of
// the terminal has lost the checkpoint, which is the part read first.
const (
	showRulings  = 10
	showRecent   = 5
	showNotes    = 3
	showLineRune = 160
)

// runShow is resume laid out for a person: one project on one screen.
//
// It reads the vault, the session store and the memory table directly rather
// than building a context pack. Build recalls memories, and recall counts each
// one it returns as used — a person glancing at the page would be reinforcing
// whatever the ranking happened to surface. Nothing here writes.
//
// Everything printed from the vault was written by some agent, so it goes
// through terminalSafe: a note is data, and a terminal acts on control bytes.
//
//	logos show [project] [--all]
func runShow(args []string) error {
	project, _ := projectArg(args)
	if project == "" {
		return fmt.Errorf("usage: logos show [project] [--all]")
	}
	all := hasFlag(args, "--all")

	ix, err := openEvents()
	if err != nil {
		return err
	}
	defer ix.Close()
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			return err
		}
	}

	cp, err := session.Latest(ix.Vault, project)
	if err != nil {
		return fmt.Errorf("reading the last checkpoint for %s: %w", project, err)
	}
	notes, err := session.Uncommitted(ix.DB, project)
	if err != nil {
		return fmt.Errorf("reading the notes since the last checkpoint: %w", err)
	}
	rulings, err := deadend.Collect(ix.Vault, ix.DB, project)
	if err != nil {
		return fmt.Errorf("reading the ruled-out approaches: %w", err)
	}
	rulings = newestRulings(rulings)
	pinned, err := memory.Pinned(ix.DB, project)
	if err != nil {
		return fmt.Errorf("reading the pinned memories: %w", err)
	}
	own, err := projectMemories(ix.DB, project)
	if err != nil {
		return fmt.Errorf("reading the memories: %w", err)
	}

	if cp == nil && len(notes) == 0 && len(rulings) == 0 && len(pinned) == 0 && len(own) == 0 {
		printNothingToResume(ix.Vault, project)
		return nil
	}

	dir := dirFor(project)
	fmt.Printf("%s — read from %s\n", project, ix.Vault)
	showCheckpoint(cp, dir)
	showNotesSince(notes)

	shown := rulings
	if !all && len(shown) > showRulings {
		shown = shown[:showRulings]
	}
	// Only the rulings on the page are measured: it is a git call per file.
	deadend.MarkDrift(shown, dir)
	showRulingList(shown, len(rulings))

	showMemories(pinned, own)

	waiting, err := memory.PendingCount(ix.DB)
	if err != nil {
		fmt.Printf("\n· review queue: not counted — %v\n", err)
		return nil
	}
	done, err := ops.AutoAcceptedSinceReview(ix.DB)
	if err != nil {
		fmt.Printf("\n· memories accepted automatically: not counted — %v\n", err)
	}
	if line := ops.QueueLine(waiting, done, "logos", ops.Plain); line != "" {
		fmt.Println("\n· " + line)
	} else {
		fmt.Println("\n· review queue: empty")
	}
	return nil
}

func showCheckpoint(cp *session.Checkpoint, dir string) {
	fmt.Println()
	if cp == nil {
		fmt.Println("Last checkpoint: none yet — `logos checkpoint` writes one")
		return
	}
	who := cp.Agent
	if who == "" {
		who = "an unnamed agent"
	}
	label := ""
	if cp.Auto {
		// The hook wrote it from the activity log: what ran, not what was learned.
		label = " — automatic, not written by an agent, nothing in it verified"
	}
	fmt.Printf("Last checkpoint — %s, %s%s\n", terminalSafe(who, 40), humanizeAgo(cp.TS), label)
	if cp.Task != "" {
		fmt.Printf("  doing: %s\n", terminalSafe(oneLineText(cp.Task), showLineRune))
	}
	if cp.Next != "" {
		fmt.Printf("  next:  %s\n", terminalSafe(oneLineText(cp.Next), showLineRune))
	}
	if g := cp.Git; g.Commit != "" {
		fmt.Printf("  at:    %s\n", terminalSafe(gitLine(g), showLineRune))
		// The same comparison resume makes: a checkpoint's commit is a claim
		// about a moment, and the reader should see it next to where the tree
		// is now. Only from inside the project's own repository.
		if dir != "" {
			if branch, commit := gitstate.Head(dir); commit != "" && commit != g.Commit {
				fmt.Printf("  now:   %s — the tree has moved since\n", gitLine(gitstate.State{Branch: branch, Commit: commit}))
			}
		}
	}
	var counts []string
	for _, c := range []struct {
		n          int
		one, other string
	}{
		{len(cp.Verified), "verified", "verified"},
		{len(cp.Decisions), "decision", "decisions"},
		{len(cp.Blockers), "blocker", "blockers"},
		{len(cp.Questions), "open question", "open questions"},
		// All an automatic checkpoint has to say: it records what ran, not why.
		{len(cp.Files), "file touched", "files touched"},
	} {
		if c.n == 1 {
			counts = append(counts, "1 "+c.one)
		} else if c.n > 1 {
			counts = append(counts, fmt.Sprintf("%d %s", c.n, c.other))
		}
	}
	if len(counts) > 0 {
		fmt.Printf("  %s\n", strings.Join(counts, " · "))
	}
	// Blockers are listed, not counted: they are what must not be built on.
	for _, b := range cp.Blockers {
		fmt.Printf("  ✗ %s\n", terminalSafe(oneLineText(b), showLineRune))
	}
}

func gitLine(g gitstate.State) string {
	s := g.Commit
	if g.Branch != "" {
		s = g.Branch + " · " + s
	}
	if g.Subject != "" {
		s += " — " + oneLineText(g.Subject)
	}
	return s
}

func showNotesSince(notes []session.Note) {
	if len(notes) == 0 {
		return
	}
	fmt.Printf("\nNot yet checkpointed — %d note%s\n", len(notes), pluralS(len(notes)))
	// Newest first: the last thing recorded is the nearest to where work stands.
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].TS > notes[j].TS })
	for i, n := range notes {
		if i == showNotes {
			fmt.Printf("  … %d older\n", len(notes)-showNotes)
			break
		}
		fmt.Printf("  · %s (%s)\n", terminalSafe(oneLineText(n.Text), showLineRune), humanizeAgo(n.TS))
	}
}

// newestRulings orders the project's dead ends newest first and keeps one of
// each: a ruling restated in every later checkpoint is still one ruling, and
// the newest restatement is the one its drift is measured from.
func newestRulings(rs []deadend.Ruling) []deadend.Ruling {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].When > rs[j].When })
	seen := map[string]bool{}
	out := rs[:0]
	for _, r := range rs {
		k := strings.ToLower(strings.Join(strings.Fields(r.Text), " "))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

func showRulingList(shown []deadend.Ruling, total int) {
	if total == 0 {
		return
	}
	fmt.Printf("\nRuled out — %d approach%s\n", total, map[bool]string{true: "", false: "es"}[total == 1])
	for _, r := range shown {
		line := r.Text
		if r.Record.Observation != "" {
			line += " — " + r.Record.Observation
		}
		who := r.Agent
		if who == "" {
			who = "an earlier agent"
		}
		fmt.Printf("  · %s (%s, %s)\n", terminalSafe(oneLineText(line), showLineRune), terminalSafe(who, 40), humanizeAgo(r.When))
		if r.Drift != nil {
			fmt.Printf("    ⚠ %s\n", terminalSafe(r.Drift.Note(), showLineRune))
		}
		if r.Stale {
			fmt.Println("    ⚠ version-bound and old enough that what it names may have moved")
		}
	}
	if total > len(shown) {
		fmt.Printf("  … %d older — logos show --all lists them\n", total-len(shown))
	}
}

// projectMemories is the project's own memories, newest first. The global ones
// are every project's, and listing them here would be the same block on every
// page; pinned ones are shown on their own — except a procedure, which Pinned
// leaves out because it never enters a pack, so it is listed here.
func projectMemories(db *sql.DB, project string) ([]memory.Memory, error) {
	all, err := memory.AllInProject(db, project)
	if err != nil {
		return nil, err
	}
	var out []memory.Memory
	for _, m := range all {
		if m.Project == project && (m.Pin == memory.PinNone || m.Kind == memory.Procedure) {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

func showMemories(pinned, own []memory.Memory) {
	if len(pinned) > 0 {
		fmt.Printf("\nPinned — %d memor%s, in every context pack\n", len(pinned), pluralY(len(pinned)))
		for _, m := range pinned {
			fmt.Printf("  · %s\n", memoryLine(m))
		}
	}
	if len(own) == 0 {
		return
	}
	fmt.Printf("\nRecent memories — %d filed under this project\n", len(own))
	for i, m := range own {
		if i == showRecent {
			fmt.Printf("  … %d older — logos memory lists them\n", len(own)-showRecent)
			break
		}
		fmt.Printf("  · %s\n", memoryLine(m))
	}
}

func memoryLine(m memory.Memory) string {
	return fmt.Sprintf("%s: %s (%s)", m.Kind, terminalSafe(oneLineText(m.Text), showLineRune), humanizeAgo(m.Created))
}

func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
